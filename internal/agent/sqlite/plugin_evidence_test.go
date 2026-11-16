package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/evidencereader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func installEvidencePlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "evidence-reader", rsi.Policy{ID: "evidence-owner-fixture", AllowedContracts: []string{contract.TextContract, contract.LinksContract}, Cases: []rsi.Case{
		{InstanceID: "evidence-reader", Contract: contract.TextContract, Input: json.RawMessage(`{"bytes":7,"boundaries":[0,3,6,7],"breaks":[7]}`), Expected: json.RawMessage(`{"bytes":7}`)},
		{InstanceID: "evidence-reader", Contract: contract.LinksContract, Input: json.RawMessage(`{"candidates":21}`), Expected: json.RawMessage(`{"take":20}`)},
	}})
}

func TestEvidencePluginOwnerExactPagingPinnedAndQuarantine(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := evidenceFixture(t)
	h := installEvidencePlugin(t, s, r)
	text := strings.Repeat("原始证据🧭\n", 80)
	ref := putArtifact(t, s, artifactOwner(t, s), text)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := evidence.Request{Ref: ref, Limit: 23}
	var got strings.Builder
	for i := 0; ; i++ {
		page, e := r.ReadEvidence(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		if page.PageRevision != status.Revision || page.Offset != q.Offset || page.Bytes > 23 || page.Text != text[q.Offset:q.Offset+int64(page.Bytes)] {
			t.Fatal("policy rewrote source or version", page)
		}
		got.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		q.Offset = *page.NextOffset
		q.PageRevision = page.PageRevision
		if i > 5000 {
			t.Fatal("pagination did not progress")
		}
	}
	if got.String() != text {
		t.Fatal("source omitted or replayed")
	}
	// Deactivation drains explicitly pinned reads; new owner work cannot fall back.
	if _, err = h.Store.Deactivate(ctx, "evidence-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: ref}); !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatal("native fallback after disabling", err)
	}
	if _, err = r.ReadEvidence(ctx, q); err != nil {
		t.Fatal("archived reader unavailable", err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadEvidence(ctx, q); !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatal("quarantined page returned", err)
	}
}

func TestEvidencePluginRejectsForgedTextPlans(t *testing.T) {
	for _, test := range []struct {
		chunk string
		total int64
		q     evidence.Request
		p     contract.TextPlan
	}{
		{"原文", 6, evidence.Request{}, contract.TextPlan{Bytes: 1}},
		{"原文", 6, evidence.Request{}, contract.TextPlan{Bytes: 0}},
		{"原文", 6, evidence.Request{}, contract.TextPlan{Bytes: 7}},
		{"abcd", 4, evidence.Request{Limit: 2}, contract.TextPlan{Bytes: 4}},
		{"abcd", 3, evidence.Request{}, contract.TextPlan{Bytes: 4}},
	} {
		if err := validateEvidenceTextPlan(test.chunk, test.total, test.q, test.p); !errors.Is(err, rsi.ErrInvalid) {
			t.Fatalf("accepted forged progress %#v: %v", test, err)
		}
	}
}

func TestEvidencePluginAcceptedMainReceiptAndLegacyRun(t *testing.T) {
	s, r, a, p := evidenceFixture(t)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	legacy := mustSubmit(t, a, "evidence-old", "original evidence 🧭")
	installEvidencePlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "source read"}, nil
		}
		return calls(evidence.ToolName, `{"ref":"current","limit":10}`), nil
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=?`, legacy.RunID) != 0 {
		t.Fatal("legacy Run changed reader")
	}
	current := mustSubmit(t, a, "evidence-new", "new original evidence 🧭")
	mustDrain(t, a)
	var results string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, current.RunID, evidence.ToolName).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(results, "new origin") || !strings.Contains(results, "page_revision") {
		t.Fatal("accepted receipt lost original bytes", results)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=?`, current.RunID) != 0 {
		t.Fatal("pure paging wrote an extra Run operation")
	}

}

func evidenceVersionPackage(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/evidencereader/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "1.1.0"`, 1)
	code = strings.Replace(code, `Bytes: end`, `Bytes: min(end, f.Boundaries[min(2,len(f.Boundaries)-1)])`, 1)
	code = strings.Replace(code, `min(20, f.Candidates)`, `min(7, f.Candidates)`, 1)
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/evidence-reader").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/evidence-reader/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = "1.1.0"
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestEvidencePluginIndependentUpgradeRollbackAndLinkCompleteness(t *testing.T) {
	ctx := context.Background()
	s, r, a, _ := evidenceFixture(t)
	h := installEvidencePlugin(t, s, r)
	old, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A pending Run freezes v1 before v1.1 is selected.
	pending := mustSubmit(t, a, "evidence-before-upgrade", "original history")
	parts := conversation.Text("exact source")
	refs := map[string]bool{}
	for i := 0; i < 25; i++ {
		ref := putArtifact(t, s, artifactOwner(t, s), "linked original")
		refs[ref] = true
		parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: ref})
	}
	message, err := r.Submit(ctx, "evidence-linked", parts)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.ReadEvidence(ctx, evidence.Request{Ref: "conversation:" + message.MessageID})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := h.Install(ctx, evidenceVersionPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "evidence-reader", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "evidence-reader", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "evidence-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "evidence-owner-v11"
	// Operator-owned expectations deliberately describe changed paging, not the
	// candidate's own output. Source bytes and accepted links stay Host facts.
	policy.Cases[0].Expected = json.RawMessage(`{"bytes":6}`)
	policy.Cases[1].Expected = json.RawMessage(`{"take":7}`)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target, policyID string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policyID, Goal: "independent evidence paging"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.ReadEvidence(ctx, evidence.Request{Ref: first.Ref, PageRevision: graph.Revision}); !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatal("unactivated page revision accepted", err)
	}
	selectVersion("evidence-upgrade", old.Revision, graph.Revision, policy.ID)
	c, err := r.Claim(ctx, pending.RunID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.readEvidence(ctx, evidence.Request{Ref: "conversation:" + message.MessageID}, c)
	if err != nil || page.Text != "exact source" || page.PageRevision != old.Revision {
		t.Fatal("old Run reinterpreted", page, err)
	}
	if err = r.Release(ctx, c); err != nil {
		t.Fatal(err)
	}
	page, err = r.ReadEvidence(ctx, evidence.Request{Ref: first.Ref})
	if err != nil || page.Text != "ex" || len(page.Links) != 7 || page.PageRevision != graph.Revision {
		t.Fatal("new binary not used", page, err)
	}
	q := evidence.Request{Ref: first.Ref, PageRevision: page.PageRevision}
	for {
		page, err = r.ReadEvidence(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range page.Links {
			if !refs[link.Ref] {
				t.Fatal("invented or duplicate link", link)
			}
			delete(refs, link.Ref)
		}
		if page.NextLinksOffset == nil {
			break
		}
		q.LinksOffset = *page.NextLinksOffset
	}
	if len(refs) != 0 {
		t.Fatal("changed page size dropped links", refs)
	}
	selectVersion("evidence-rollback", graph.Revision, old.Revision, "evidence-owner-fixture")
	if err = h.Store.Retire(ctx, pkg.Digest); err != nil {
		t.Fatal(err)
	}
	again, err := r.ReadEvidence(ctx, evidence.Request{Ref: first.Ref})
	if err != nil || again.Text != first.Text || len(again.Links) != 20 {
		t.Fatal("rollback changed source projection", again, err)
	}
	retained, err := r.ReadEvidence(ctx, evidence.Request{Ref: first.Ref, PageRevision: graph.Revision})
	if err != nil || retained.Text != "ex" || len(retained.Links) != 7 {
		t.Fatal("retired version lost existing reference", retained, err)
	}
}
