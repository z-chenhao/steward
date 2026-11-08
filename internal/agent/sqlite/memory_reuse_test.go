package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestRecallUnifiedVisibilityIncludesSealedReadsAndAutomaticEvidence(t *testing.T) {
	ctx := context.Background()
	call := llm.ToolCall{ID: "read", Name: memory.ManageName, Arguments: `{"operation":"read","memory_ref":"memory:reuse"}`}
	s, r, c, executor, exchange := admitPolicy(t, []tool.Definition{memory.Definition()}, []llm.ToolCall{call})
	store, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(store, vault)
	_, e := store.WriteDocument(ctx, recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}, 0, "reuse", "reuse", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:reuse"}}, memory.DocumentWrite{Layer: memory.Notes, Title: "Receipt rule", Content: "Bind receipt, review, draft only."})
	if e != nil {
		t.Fatal(e)
	}
	result, e := executor.Execute(ctx, exchange, []llm.ToolCall{call})
	if e != nil || result[0].Error != nil {
		t.Fatal(result, e)
	}
	run, e := r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	visible := []llm.Message{{Role: "tool", ToolCallID: call.ID, Text: result[0].Text}}
	for _, messages := range [][]llm.Message{visible, memoryReadProjection(append([]llm.Message(nil), visible...))} {
		refs, e := r.visibleEvidenceRefs(ctx, run, messages)
		if e != nil || len(refs) != 1 || refs[0] != "memory:reuse" {
			t.Fatal(refs, e)
		}
	}
	refs, e := r.visibleEvidenceRefs(ctx, run, []llm.Message{{Role: "tool", ToolCallID: "forged", Text: result[0].Text}})
	if e != nil || len(refs) != 0 {
		t.Fatal(refs, e)
	}
	hits := []recall.Hit{{SourceRef: "conversation:original", SourceType: "conversation", Content: "Confirmed original event", OccurredAt: time.Now()}}
	raw, _ := json.Marshal(hits)
	if _, e = s.db.Exec(`INSERT INTO run_recall VALUES(?,99,'test','query',?,?)`, run.ID, string(raw), time.Now().UnixMicro()); e != nil {
		t.Fatal(e)
	}
	refs, e = r.visibleEvidenceRefs(ctx, run, []llm.Message{{Role: "user", Text: "current" + renderInputRecall(hits)}})
	if e != nil || len(refs) != 1 || refs[0] != "conversation:original" {
		t.Fatal(refs, e)
	}
	refs, e = r.visibleEvidenceRefs(ctx, run, nil)
	if e != nil || len(refs) != 0 {
		t.Fatal("compacted evidence stayed excluded", refs, e)
	}
	current := llm.Message{Role: "user", Text: "current", Source: &llm.ContextSource{Kind: "task", Ref: "task:visible-input"}}
	prepared, _ := json.Marshal([]llm.Message{{Role: "user", Text: "current" + renderInputRecall(hits), Source: current.Source}})
	provenance := `{"recall_input_ref":"task:visible-input"}`
	if _, e = s.db.Exec(`INSERT INTO context_compaction VALUES(?,1000,'policy','input',?,?,?)`, run.ID, string(prepared), provenance, time.Now().UnixMicro()); e != nil {
		t.Fatal(e)
	}
	run.Checkpoint.Step = 1000
	refs, e = r.visibleEvidenceRefs(ctx, run, []llm.Message{current})
	if e != nil || len(refs) != 1 || refs[0] != "conversation:original" {
		t.Fatal("inline evidence lost between preparation and checkpoint", refs, e)
	}
	compacted, _ := json.Marshal([]llm.Message{current})
	if _, e = s.db.Exec(`INSERT INTO context_compaction VALUES(?,1001,'policy','input',?,?,?)`, run.ID, string(compacted), provenance, time.Now().UnixMicro()); e != nil {
		t.Fatal(e)
	}
	run.Checkpoint.Step = 1001
	refs, e = r.visibleEvidenceRefs(ctx, run, []llm.Message{current})
	if e != nil || len(refs) != 0 {
		t.Fatal("old prepared evidence survived compaction", refs, e)
	}
	partial := `{"ref":"original","layer":"transcripts","text":"tail","total_bytes":4,"offset":10}`
	parsed, e := evidenceHits(partial, func(v llm.MemoryText) (string, error) { return r.memoryResolver(run.ID)(ctx, v) })
	if e != nil || len(parsed) != 1 || completeRecallHit(parsed[0]) {
		t.Fatal(parsed, e)
	}
}

func TestRecallRepeatedReadReusesVisibleEvidenceWithoutDuplicatingBody(t *testing.T) {
	s, r, actor, p := childFixture(t, memory.Definition())
	ctx := context.Background()
	store, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(store, vault)
	body := "Verified CSV import rule. " + strings.Repeat("preserve evidence ", 70)
	doc, e := store.WriteDocument(ctx, recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}, 0, "repeat", "repeat", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:repeat"}}, memory.DocumentWrite{Layer: memory.Notes, Title: "Import rule", Content: body})
	if e != nil {
		t.Fatal(e)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		messages, e := llm.ResolveMemoryMessages(ctx, req)
		if e != nil {
			t.Error(e)
			return llm.GenerationResult{}, e
		}
		last := messages[len(messages)-1]
		switch last.ToolCallID {
		case "first":
			if !strings.Contains(last.Text, body) {
				t.Error("first read lost body", last.Text)
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "second", Name: memory.ManageName, Arguments: `{"operation":"read","memory_ref":"` + doc.Ref + `"}`}}}, nil
		case "second":
			if !strings.Contains(last.Text, `"already_visible":true`) || len(last.Text) > 200 {
				t.Error("repeated full body", last.Text)
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return llm.GenerationResult{Text: "Reused verified rule."}, nil
		default:
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "first", Name: memory.ManageName, Arguments: `{"operation":"read","memory_ref":"` + doc.Ref + `"}`}}}, nil
		}
	}
	mustSubmit(t, actor, "reuse-read", "Consult the import rule")
	mustDrain(t, actor)
	if scalar(t, s, `SELECT count(*) FROM memory_access WHERE operation='read'`) != 2 {
		t.Fatal("access receipts missing")
	}
}

func TestRecallCompleteSearchStillAllowsReadingCASMetadata(t *testing.T) {
	s, r, actor, p := childFixture(t, memory.Definition(), recall.Definition(recall.UnconfiguredIdentity))
	ctx := context.Background()
	store, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(store, vault)
	store.ConfigureLocal()
	r.ConfigureRecall(recall.Service{Memory: store, Conversation: &explicitRecallProbe{}})
	doc, e := store.WriteDocument(ctx, recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}, 0, "metadata", "metadata", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:metadata"}}, memory.DocumentWrite{Layer: memory.Notes, Title: "CSV import", Content: "Verified CSV import rule without BOM."})
	if e != nil {
		t.Fatal(e)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		messages, e := llm.ResolveMemoryMessages(ctx, req)
		if e != nil {
			t.Error(e)
			return llm.GenerationResult{}, e
		}
		last := messages[len(messages)-1]
		switch last.ToolCallID {
		case "search":
			if !strings.Contains(last.Text, "without BOM") {
				t.Error(last.Text)
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "metadata-read", Name: memory.ManageName, Arguments: `{"operation":"read","memory_ref":"` + doc.Ref + `"}`}}}, nil
		case "metadata-read":
			if !strings.Contains(last.Text, `"revision":1`) || strings.Contains(last.Text, `"already_visible":true`) {
				t.Error("body-only search suppressed CAS metadata", last.Text)
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			return llm.GenerationResult{Text: "Revision available for correction."}, nil
		default:
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "search", Name: recall.ToolName, Arguments: `{"query":"CSV import"}`}}}, nil
		}
	}
	mustSubmit(t, actor, "read-metadata", "Find the import rule and its revision")
	mustDrain(t, actor)
}
