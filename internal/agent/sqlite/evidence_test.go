package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func evidenceFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider) {
	t.Helper()
	s, r, _, a, p := loopFixture(t)
	defs := append(task.Definitions(), artifacttools.Definitions()...)
	registry, err := tool.NewRegistry(append(defs, evidence.Definition()))
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	return s, r, a, p
}

func hasEvidenceLink(page evidence.Page, ref string) bool {
	for _, link := range page.Links {
		if link.Ref == ref {
			return true
		}
	}
	return false
}

func TestEvidenceOwnerResolvesOrdinaryObjectID(t *testing.T) {
	s, r, _, _ := evidenceFixture(t)
	ref := putArtifact(t, s, artifactOwner(t, s), "exact original file")
	page, err := r.ReadEvidence(context.Background(), evidence.Request{Kind: "artifact", Ref: tool.ObjectID(ref)})
	if err != nil || page.Text != "exact original file" {
		t.Fatal("ordinary ID was validated but not resolved", page, err)
	}
}

func TestEvidenceExplicitMessageTaskArtifactChainAndRestart(t *testing.T) {
	testEvidenceChain(t, false)
}
func TestEvidencePluginExplicitMessageTaskArtifactChainAndRestart(t *testing.T) {
	testEvidenceChain(t, true)
}
func testEvidenceChain(t *testing.T, managed bool) {
	ctx := context.Background()
	s, r, a, p := evidenceFixture(t)
	if managed {
		installEvidencePlugin(t, s, r)
	}
	text := strings.Repeat("原始证据🧭", 1200)
	attachment := putArtifact(t, s, artifactOwner(t, s), text)
	mainStage := 0
	p.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(request) {
			for _, spec := range request.Tools {
				if spec.Name == evidence.ToolName {
					t.Error("Worker received Main conversation navigation")
				}
			}
			return llm.GenerationResult{Text: "finished delegated work"}, nil
		}
		if isFollowup(request) {
			return llm.GenerationResult{Text: "delivered"}, nil
		}
		mainStage++
		if mainStage == 1 {
			return calls(evidence.ToolName, `{"ref":"current"}`), nil
		}
		if mainStage > 3 {
			return llm.GenerationResult{Text: "delegated"}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		var current evidence.Page
		if mainStage == 3 {
			if json.Unmarshal([]byte(last.Text), &current) != nil || current.Kind != "artifact" || current.Ref != tool.ObjectID(attachment) || !strings.HasPrefix(current.Text, "原始证据🧭") {
				t.Error("ordinary artifact ID could not reopen its exact content", last.Text)
			}
			return calls(task.ManageName, `{"operation":"create","task":"Check this specific attached evidence.","artifactRefs":["`+tool.ObjectID(attachment)+`"]}`), nil
		}
		if json.Unmarshal([]byte(last.Text), &current) != nil || current.Text != "Use this original file." || !hasEvidenceLink(current, tool.ObjectID(attachment)) || strings.Contains(last.Text, "artifact:") || strings.Contains(last.Text, "conversation:") {
			t.Fatal("Main current-input Tool did not navigate the authorized original", last.Text)
		}
		return calls(evidence.ToolName, `{"kind":"artifact","ref":"`+tool.ObjectID(attachment)+`"}`), nil
	}
	receipt, err := r.Submit(ctx, "chain", []conversation.ContentPart{{Type: "text", Text: "Use this original file."}, {Type: "file", ArtifactRef: attachment}})
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	var taskRef string
	if err = s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&taskRef); err != nil {
		t.Fatal(err)
	}
	messageRef := "conversation:" + receipt.MessageID
	message, err := r.ReadEvidence(ctx, evidence.Request{Ref: messageRef})
	if err != nil || message.Text != "Use this original file." || !hasEvidenceLink(message, taskRef) || !hasEvidenceLink(message, attachment) {
		t.Fatal(message, err)
	}
	work, err := r.ReadEvidence(ctx, evidence.Request{Ref: taskRef})
	if err != nil || !hasEvidenceLink(work, messageRef) || !hasEvidenceLink(work, attachment) {
		t.Fatal(work, err)
	}
	var joined strings.Builder
	q := evidence.Request{Ref: attachment}
	for {
		page, err := r.ReadEvidence(ctx, q)
		if err != nil || !hasEvidenceLink(page, taskRef) || page.Bytes > 4096 {
			t.Fatal(page, err)
		}
		joined.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		q.Offset = *page.NextOffset
	}
	if joined.String() != text {
		t.Fatal("summary-external original was not read exactly")
	}
	// A cyclic message/task/artifact association is returned as links only;
	// this one-hop reader never recursively expands the graph.
	if len(work.Links) > 20 || len(message.Links) > 20 {
		t.Fatal("unbounded relationship expansion")
	}
	s2, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	r2 := s2.Runs(r.history, r.registry)
	if managed {
		r2.ConfigurePlugins(r.plugins.Catalog)
	}
	again, err := r2.ReadEvidence(ctx, evidence.Request{Ref: messageRef})
	if err != nil || !reflect.DeepEqual(message, again) {
		t.Fatal("restart changed immutable source projection", again, err)
	}
}

func TestEvidenceHostInvocationFrozenReplayAndNoGrant(t *testing.T) {
	ctx := context.Background()
	call := []llm.ToolCall{{ID: "read", Name: evidence.ToolName, Arguments: `{"ref":"conversation:explicit-original","limit":4}`}}
	s, r, c, executor, exchange := admitPolicy(t, []tool.Definition{evidence.Definition()}, call)
	defer r.Release(ctx, c)
	if err := r.history.Append(ctx, []conversation.Message{{MessageID: "explicit-original", ConversationID: idAgent(t, s), Role: "user", Content: conversation.Text("do the requested work"), Source: "cli", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	hidden := putArtifact(t, s, artifactOwner(t, s), "never authorized for this Main")
	if err := r.history.Append(ctx, []conversation.Message{{MessageID: "hidden-link", ConversationID: idAgent(t, s), Role: "user", Content: []conversation.ContentPart{{Type: "text", Text: "explicit reference grants no access"}, {Type: "file", ArtifactRef: hidden}}, Source: "cli", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	linked, err := r.readEvidence(ctx, evidence.Request{Ref: "conversation:hidden-link"}, c)
	if err != nil || hasEvidenceLink(linked, hidden) {
		t.Fatal("ungranted object exposed as a navigation link", linked, err)
	}
	if _, err = r.readEvidence(ctx, evidence.Request{Ref: hidden}, c); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("reference alone granted access", err)
	}
	before := scalar(t, s, `SELECT count(*) FROM artifact_reference`)
	out, err := executor.Execute(ctx, exchange, call)
	if err != nil || len(out) != 1 || out[0].Error != nil {
		t.Fatal(out, err)
	}
	var page evidence.Page
	if json.Unmarshal([]byte(out[0].Text), &page) != nil || page.Text != "do t" || page.NextOffset == nil {
		t.Fatal(out)
	}
	if scalar(t, s, `SELECT count(*) FROM artifact_reference`) != before {
		t.Fatal("evidence reader granted new Artifact access")
	}
	if _, err = r.Submit(ctx, "new-input", conversation.Text("new unrelated input")); err != nil {
		t.Fatal(err)
	}
	again, err := executor.Execute(ctx, exchange, call)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("accepted evidence call reread changing state on replay", again, err)
	}
}

func TestEvidenceLinksRejectHiddenMissingExpiredAndRevokedSources(t *testing.T) {
	testEvidencePrivacy(t, false)
}
func TestEvidencePluginLinksRejectHiddenMissingExpiredAndRevokedSources(t *testing.T) {
	testEvidencePrivacy(t, true)
}
func testEvidencePrivacy(t *testing.T, managed bool) {
	ctx := context.Background()
	s, r, _, _ := evidenceFixture(t)
	if managed {
		installEvidencePlugin(t, s, r)
	}
	owner := artifactOwner(t, s)
	private := putArtifact(t, s, owner, "ungranted private file")
	input, err := r.Submit(ctx, "privacy", []conversation.ContentPart{{Type: "text", Text: "fresh evidence"}, {Type: "file", ArtifactRef: private}})
	if err != nil {
		t.Fatal(err)
	}
	// Keep an unattached local-owner object to exercise a concurrent privacy
	// barrier separately from the attachment's expiry.
	other := putArtifact(t, s, owner, "never attached")
	page, err := r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:" + input.MessageID})
	if err != nil || !hasEvidenceLink(page, private) {
		t.Fatal(page, err)
	}
	if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:missing"}); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("missing source", err)
	}
	if err = r.history.Append(ctx, []conversation.Message{{MessageID: "foreign", ConversationID: "other-agent", Role: "user", Content: conversation.Text("foreign text"), Source: "cli", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:foreign"}); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("cross-scope source exposed", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER artifact_immutable; UPDATE artifact SET expires_at=1 WHERE artifact_ref=?`, private); err != nil {
		t.Fatal(err)
	}
	page, err = r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:" + input.MessageID})
	if err != nil || hasEvidenceLink(page, private) {
		t.Fatal("expired ref exposed as a link", page, err)
	}
	if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: private}); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("expired source read", err)
	}
	// The privacy barrier can arrive while an otherwise allowed read runs.
	s.MainBoundary = func(name string) {
		if name != "after_evidence_read" {
			return
		}
		s.MainBoundary = nil
		if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner.AgentID) }); err != nil {
			t.Fatal(err)
		}
	}
	if page, err = r.ReadEvidence(ctx, evidence.Request{Ref: other}); !errors.Is(err, agent.ErrClaimLost) || page.Text != "" {
		t.Fatal("late read crossed Forget", page, err)
	}
	for _, ref := range []string{other, "conversation:" + input.MessageID} {
		if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: ref}); !errors.Is(err, artifact.ErrNotFound) {
			t.Fatal("revoked source revived", ref, err)
		}
	}
	if err = r.history.Append(ctx, []conversation.Message{{MessageID: "fresh-source", ConversationID: owner.AgentID, Role: "user", Content: []conversation.ContentPart{{Type: "text", Text: "fresh source with revoked reference"}, {Type: "file", ArtifactRef: other}}, Source: "cli", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	page, err = r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:fresh-source"})
	if err != nil || hasEvidenceLink(page, other) {
		t.Fatal("fresh message reintroduced revoked link", page, err)
	}
}

func TestEvidenceLinksBoundedPagesAndOldCatalogSelection(t *testing.T) {
	testEvidenceLinkPages(t, false)
}
func TestEvidencePluginLinksBoundedPagesAndOldCatalogSelection(t *testing.T) {
	testEvidenceLinkPages(t, true)
}
func testEvidenceLinkPages(t *testing.T, managed bool) {
	ctx := context.Background()
	s, r, _, _ := evidenceFixture(t)
	if managed {
		installEvidencePlugin(t, s, r)
	}
	parts := conversation.Text("all explicit attachments")
	expected := map[string]bool{}
	for i := 0; i < 25; i++ {
		ref := putArtifact(t, s, artifactOwner(t, s), fmt.Sprint("attachment ", i))
		expected[ref] = true
		parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: ref})
	}
	input, err := r.Submit(ctx, "pages", parts)
	if err != nil {
		t.Fatal(err)
	}
	q := evidence.Request{Ref: "conversation:" + input.MessageID}
	first, err := r.ReadEvidence(ctx, q)
	if err != nil || len(first.Links) != 20 || first.NextLinksOffset == nil {
		t.Fatal(first, err)
	}
	q.LinksOffset = *first.NextLinksOffset
	last, err := r.ReadEvidence(ctx, q)
	if err != nil || len(last.Links) != 5 || last.NextLinksOffset != nil {
		t.Fatal(last, err)
	}
	for _, link := range append(first.Links, last.Links...) {
		if !expected[link.Ref] {
			t.Fatal("duplicate or unrelated link", link)
		}
		delete(expected, link.Ref)
	}
	if len(expected) != 0 {
		t.Fatal("pagination lost existing relationship")
	}
	// A separate process/pool may hold the reserved WAL writer. Read-only
	// navigation, including per-link authorization and file verification, must
	// use deferred snapshots rather than competing for that writer lock.
	writerStore, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer writerStore.Close()
	writer, err := writerStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	readPage, err := r.ReadEvidence(readCtx, evidence.Request{Ref: "conversation:" + input.MessageID})
	if err != nil || len(readPage.Links) != 20 {
		t.Fatal("evidence page contended for WAL writer", err)
	}
	if _, err = r.ReadEvidence(readCtx, evidence.Request{Ref: readPage.Links[0].Ref}); err != nil {
		t.Fatal("evidence Artifact read contended for WAL writer", err)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	old := tool.FixtureRegistry()
	d, ok := old.Resolve("mcp__fixture__echo")
	if !ok {
		t.Fatal("missing prior capability")
	}
	upgraded, err := tool.NewRegistry([]tool.Definition{d, evidence.Definition()})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := upgraded.Select(old.Describe())
	if err != nil || len(selected.Describe()) != 1 || selected.Describe()[0].Name == evidence.ToolName {
		t.Fatal("upgrade changed old frozen capability", err)
	}
}

func TestEvidenceAdmittedMainReadDoesNotReserveWriter(t *testing.T) {
	testEvidenceAdmittedRead(t, false)
}
func TestEvidencePluginAdmittedMainReadDoesNotReserveWriter(t *testing.T) {
	testEvidenceAdmittedRead(t, true)
}
func testEvidenceAdmittedRead(t *testing.T, managed bool) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "read", Name: evidence.ToolName, Arguments: `{"ref":"conversation:writer-original"}`}}
	s, r, c, _, exchange := admitPolicyRun(t, []tool.Definition{evidence.Definition()}, calls, false, func(s *Store, r *Runs) {
		if managed {
			installEvidencePlugin(t, s, r)
		}
	})
	defer r.Release(ctx, c)
	if err := r.history.Append(ctx, []conversation.Message{{MessageID: "writer-original", ConversationID: idAgent(t, s), Role: "user", Content: conversation.Text("read this original"), Source: "cli", CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	ts := r.ToolStore(c).(*tools)
	invs, err := ts.Accept(ctx, exchange, calls, r.registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ts.Prepare(ctx, invs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = ts.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	// Admission and Start need writes; the already-admitted handler only reads.
	// Hold a separate pool's WAL writer after those necessary writes finish.
	writerStore, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer writerStore.Close()
	writer, err := writerStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	readCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	out, err := ts.ExecuteBuiltin(readCtx, inv)
	if err != nil || out.Error != nil {
		t.Fatal("already-admitted Main evidence read contended for WAL writer", out, err)
	}
	var page evidence.Page
	if err = json.Unmarshal([]byte(out.Text), &page); err != nil || page.Text != "read this original" {
		t.Fatal("admitted Main did not read the original evidence", page, err)
	}
}

func TestEvidenceInvalidTypedReferenceReturnsInvalidArgument(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "invalid", Name: evidence.ToolName, Arguments: `{"ref":"memory:unsupported"}`}}
	_, r, c, executor, exchange := admitPolicy(t, []tool.Definition{evidence.Definition()}, calls)
	defer r.Release(ctx, c)
	out, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(out) != 1 || out[0].Error == nil || out[0].Error.Code != "INVALID_ARGUMENT" {
		t.Fatal("invalid input reported as authorization denial", out, err)
	}
}
