package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestLayeredMemoryWorkerWritesNotesWithoutGainingForgetControl(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{
		{ID: "note", Name: memory.ManageName, Arguments: `{"operation":"write","layer":"notes","title":"A tested method","content":"Tentative method; validate the outcome before reuse."}`},
		{ID: "forget", Name: memory.ManageName, Arguments: `{"operation":"forget","memory_ref":"missing"}`},
		{ID: "dream", Name: memory.ManageName, Arguments: `{"operation":"dream"}`},
	}
	native, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	native.Spec.Name = "builtin__read"
	s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{memory.Definition(), native}, calls)
	m, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(m, vault)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 3 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	for _, result := range results[1:] {
		if result.Error == nil || result.Error.Code != "PERMISSION_DENIED" {
			t.Fatal("Worker gained control", result)
		}
	}
	docs, err := m.ListDocuments(ctx, recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}, 0, memory.Notes, 20)
	if err != nil || len(docs) != 1 || !strings.Contains(docs[0].Content, "Tentative method") {
		t.Fatal(docs, err)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_privacy`) != 0 {
		t.Fatal("note write changed privacy epoch")
	}
}

func TestLayeredMemoryInjectedSnapshotFrozenSealedAndLoadedWithoutSearch(t *testing.T) {
	s, r, _, a, _ := semanticRuntime(t)
	ctx := context.Background()
	link, err := r.ManageMemory(ctx, "navigation-bootstrap", memory.Mutation{Operation: "write", Layer: memory.Representations, Title: "Unrelated question", Content: "PRIVATE_LINK_CONTENT: exact bounded evidence"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := r.ManageMemory(ctx, "bootstrap", memory.Mutation{Operation: "write", Layer: memory.Injected, Title: "Session memory", Content: "PRIVATE_BOOTSTRAP_MARKER: answer concisely.", Links: []string{link["ref"].(string)}})
	if err != nil {
		t.Fatal(err)
	}
	receipt := mustSubmit(t, a, "question", "An unrelated question with no search matches")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := r.Steering(ctx, c, run.Checkpoint.InputSequence)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{Type: "user", MessageID: input.Message.MessageID, Content: input.Message.Content})
		run.Checkpoint.InputSequence = input.Sequence
	}
	if err = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := llm.ResolveMemoryMessages(ctx, gen.Request)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(wire)
	if !strings.Contains(string(text), "PRIVATE_BOOTSTRAP_MARKER") || !strings.Contains(string(text), link["ref"].(string)) || strings.Contains(string(text), "PRIVATE_LINK_CONTENT") {
		t.Fatal("bootstrap depended on recall", string(text))
	}
	preview, err := s.Inspector().ChatPreview(ctx, observability.ObservationQuery{RunID: run.ID, Selected: "generation:" + gen.Request.GenerationID})
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, message := range preview {
		marked = marked || strings.Contains(message.Text, "Encrypted memory content")
		if strings.Contains(message.Text, "PRIVATE_BOOTSTRAP_MARKER") || strings.Contains(message.Text, "sealed:") {
			t.Fatal("preview exposed sealed memory", message)
		}
	}
	if !marked {
		t.Fatal("preview made sealed memory look empty", preview)
	}
	var stored string
	if err = s.db.QueryRow(`SELECT request_json FROM model_generation WHERE run_id=?`, run.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "PRIVATE_BOOTSTRAP_MARKER") {
		t.Fatal("plaintext memory in persisted request")
	}
	_, err = r.ManageMemory(ctx, "change-bootstrap", memory.Mutation{Operation: "write", Layer: memory.Injected, Title: "Changed memory", Content: "NEW_BOOTSTRAP_MARKER", MemoryRef: created["ref"].(string), Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal("background edit changed frozen request", err)
	}
	first, _ := json.Marshal(gen.Request.Messages)
	second, _ := json.Marshal(again.Request.Messages)
	if string(first) != string(second) {
		t.Fatal("session memory snapshot changed")
	}
	_, err = r.ManageMemory(ctx, "forget-bootstrap", memory.Mutation{Operation: "forget", MemoryRef: created["ref"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Generation(ctx, c, run); err == nil {
		t.Fatal("forget did not revoke unfinished memory context")
	}
}

func TestRecallEmptyEvidenceOmittedFromAgentMessagesAndFrozen(t *testing.T) {
	s, r, _, a, _ := semanticRuntime(t)
	ctx := context.Background()
	registry, err := tool.NewRegistry([]tool.Definition{recall.Definition("empty-test")})
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	r.ConfigureRecall(recall.Service{Memory: recall.EmptyMemory{}, Conversation: recall.EmptyMemory{}})
	receipt := mustSubmit(t, a, "empty-evidence", "Current request")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	input := agent.TextMessage("user", "Current request")
	input.MessageID = "empty-evidence-input"
	run.Checkpoint.Messages = append(run.Checkpoint.Messages, input)
	if err = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Request.Messages) != 2 || first.Request.Messages[0].Role != "system" || first.Request.Messages[1].Text != "Current request" {
		t.Fatal("empty evidence added an agent message", first.Request.Messages)
	}
	for _, m := range first.Request.Messages {
		if m.Memory != nil || m.Origin == "runtime" {
			t.Fatal("empty evidence retained in agent messages", m)
		}
	}
	var hits, sealed string
	if err = s.db.QueryRow(`SELECT hits_json FROM run_recall WHERE run_id=?`, run.ID).Scan(&hits); err != nil || hits != "[]" {
		t.Fatal("empty retrieval was not frozen", hits, err)
	}
	if err = s.db.QueryRow(`SELECT content_sealed FROM run_injected_memory WHERE run_id=?`, run.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if plain, e := r.vault.Open(ctx, sealed, "memory-injected:"+run.ID); e != nil || plain != "" {
		t.Fatal("empty injection was not frozen", e)
	}
	if _, err = r.ManageMemory(ctx, "late-memory", memory.Mutation{Operation: "write", Layer: memory.Injected, Title: "Session memory", Content: "LATE_MEMORY"}); err != nil {
		t.Fatal(err)
	}
	// A continuation must reuse both empty snapshots, even if retrieval is now unavailable.
	r.ConfigureRecall(recall.Service{})
	run.Checkpoint.Step++
	if err = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(first.Request.Messages)
	after, _ := json.Marshal(next.Request.Messages)
	if string(before) != string(after) || scalar(t, s, `SELECT count(*) FROM run_recall WHERE run_id=?`, run.ID) != 1 {
		t.Fatal("continuation refreshed empty evidence", string(after))
	}
	assertFrozenContextRun(t, s, run.ID)
}

func TestLayeredMemoryDreamBuildsAllRepresentationsAndRecoversAcceptedPasses(t *testing.T) {
	s, r, m, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "evidence", "请你调研这个问题")
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			t.Fatal("unexpected fact extraction")
		}
		proposal := memory.Proposal{Facts: []memory.Candidate{}}
		switch in.Pass {
		case 0:
			proposal.Documents = []memory.DocumentWrite{{Layer: memory.Notes, Support: []memory.SupportQuote{{Observation: 0, Quote: in.Observations[0].Text}}, Title: "Research lesson", Content: "Tentative: preserve the research constraints and check the evidence."}}
		case 1:
			proposal.Documents = []memory.DocumentWrite{{Layer: memory.Representations, Title: "Research wiki", Content: "Research evidence and open questions", Links: []string{in.Documents[0].Ref}}}
		case 2:
			var wiki string
			for _, d := range in.Documents {
				if d.Layer == memory.Representations {
					wiki = d.Ref
				}
			}
			proposal.Documents = []memory.DocumentWrite{{Layer: memory.Injected, Title: "Session memory", Content: "Use the research wiki for the evidence and unresolved questions.", Links: []string{wiki}}}
			proposal.Done = true
		}
		raw, _ := json.Marshal(proposal)
		return llm.GenerationResult{Text: string(raw), Usage: &llm.TokenUsage{InputTokens: 50, OutputTokens: 25}}
	}
	if _, err := r.ManageMemory(ctx, "dream", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	before := scalar(t, s, `SELECT count(*) FROM run`)
	if err := r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 || scalar(t, s, `SELECT count(*) FROM memory_generation WHERE purpose='dream' AND status='completed'`) != 3 {
		t.Fatal("dream passes/usage missing", p.calls)
	}
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	for _, layer := range []string{memory.Notes, memory.Representations, memory.Injected} {
		docs, e := m.ListDocuments(ctx, scope, 0, layer, 20)
		if e != nil || len(docs) != 1 {
			t.Fatal(layer, docs, e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != before || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("maintenance manufactured user work")
	}
	// Re-delivery after the domain and step commits must not call the model again.
	if _, err := s.db.Exec(`DELETE FROM memory_semantic_receipt WHERE event_id IN(SELECT event_id FROM memory_mutation WHERE source_type='dream')`); err != nil {
		t.Fatal(err)
	}
	if err := r.CurateMemory(ctx); err != nil || p.calls != 3 {
		t.Fatal("accepted dream replayed", err, p.calls)
	}
	page, err := r.InspectMemory(ctx, memory.InspectionQuery{Layer: memory.Transcripts}, m)
	if err != nil || len(page.Items) != 1 || page.Items[0].Layer != memory.Transcripts {
		t.Fatal(page, err)
	}
}

func mustOwner(t *testing.T, s *Store) string {
	t.Helper()
	var owner string
	if err := s.db.QueryRow(`SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestLayeredMemoryDreamContinuesLongTranscriptsWithoutLosingUTF8(t *testing.T) {
	s, r, _, _, p := semanticRuntime(t)
	ctx := context.Background()
	text := "请你" + strings.Repeat("资料", 10000)
	if _, err := r.Submit(ctx, "long-transcript", conversation.Text(text)); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var parts strings.Builder
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" || len(in.Observations) != 1 {
			t.Fatal("unexpected maintenance input", in.Mode, len(in.Observations))
		}
		o := in.Observations[0]
		if o.Offset != int64(parts.Len()) {
			t.Fatal("gap or replay in transcript scan", o.Offset, parts.Len())
		}
		parts.WriteString(o.Text)
		return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
	}
	if _, err := r.ManageMemory(ctx, "long-dream", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10 && parts.Len() < len(text)+1; i++ {
		if err := r.CurateMemory(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if parts.String() != text+"\n" || p.calls < 2 {
		t.Fatal("long transcript was truncated", parts.Len(), len(text), p.calls)
	}
	if scalar(t, s, `SELECT next_offset FROM memory_dream_coverage`) != 0 {
		t.Fatal("partial coverage never finished")
	}
}

func TestLayeredMemoryDocumentToolSurvivesNestedDelegation(t *testing.T) {
	s, _, a, p := childFixture(t, memory.Definition())
	p.respond = childPolicy
	mustSubmit(t, a, "layered-tree", "delegate with memory")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id, json_each(c.tools_json) t WHERE r.worker_position='child' AND r.status='completed' AND json_extract(t.value,'$.name')='builtin__memory_manage' AND json_extract(t.value,'$.version')=6`) != 2 {
		t.Fatal("document tool did not survive the frozen child capability guard")
	}
}

func TestLayeredMemoryDreamRebindsEditedLinksAndReplaysOriginalReceipts(t *testing.T) {
	s, r, m, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "source", "请你研究以下方法")
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	seed, err := r.ManageMemory(ctx, "seed", memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Original", Content: "Tentative method"})
	if err != nil {
		t.Fatal(err)
	}
	old := seed["ref"].(string)
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			t.Fatal("unexpected extraction")
		}
		// Deliberately put the dependent wiki before the edited note.
		raw, _ := json.Marshal(memory.Proposal{Facts: []memory.Candidate{}, Done: true, Documents: []memory.DocumentWrite{
			{Layer: memory.Representations, Title: "Wiki", Content: "Verified constraints", Links: []string{old}},
			{Ref: old, Revision: 1, Layer: memory.Notes, Support: []memory.SupportQuote{{Observation: 0, Quote: in.Observations[0].Text}}, Title: "Updated", Content: "Corrected method"},
			{Layer: memory.Injected, Title: "Session", Content: "Remember the method", Links: []string{old}},
		}})
		return llm.GenerationResult{Text: string(raw)}
	}
	if _, err = r.ManageMemory(ctx, "dream-rebind", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	if err = r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	docs, err := m.ListDocuments(ctx, scope, 0, "", 20)
	if err != nil || len(docs) != 3 {
		t.Fatal(docs, err)
	}
	var updated string
	for _, d := range docs {
		if d.Layer == memory.Notes {
			updated = d.Ref
			if d.Revision != 2 || d.Ref == old {
				t.Fatal(d)
			}
		}
	}
	if updated == "" {
		t.Fatal("edited note missing")
	}
	refs := map[string]string{}
	for _, d := range docs {
		refs[d.Layer] = d.Ref
		if d.Layer != memory.Notes && (len(d.Links) != 1 || d.Links[0] != updated) {
			t.Fatal("stale dependency", d)
		}
	}
	// Simulate memory.db committing, then losing only the state-side acceptance
	// receipt and coverage. Replay must retain original proposal-index identities.
	for _, query := range []string{
		`DELETE FROM memory_dream_step`, `DELETE FROM memory_dream_coverage`,
		`DELETE FROM memory_semantic_receipt WHERE event_id IN(SELECT event_id FROM memory_mutation WHERE source_type='dream')`,
		`UPDATE memory_mutation SET applied_at=NULL WHERE source_type='dream'`,
	} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.CurateMemory(ctx); err != nil || p.calls != 1 {
		t.Fatal("accepted proposal was regenerated", err, p.calls)
	}
	after, err := m.ListDocuments(ctx, scope, 0, "", 20)
	if err != nil || len(after) != 3 {
		t.Fatal(after, err)
	}
	for _, d := range after {
		if refs[d.Layer] != d.Ref {
			t.Fatal("replay duplicated document", d)
		}
	}
}
