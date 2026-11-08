package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"strings"
	"testing"
	"time"
)

func TestMemoryDreamRetrievesOldRelevantNoteAndPinsInjectedHead(t *testing.T) {
	s, r, store, _, provider := semanticRuntime(t)
	ctx := context.Background()
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	sources := []memory.SourceRef{{Type: "feedback", Ref: "feedback:verified"}}
	old, e := store.WriteDocument(ctx, scope, 0, "old", "old", time.Now(), sources, memory.DocumentWrite{Layer: memory.Notes, Title: "CSV quasar import", Content: "Previously verified CSV quasar import uses UTF-8 without BOM."})
	if e != nil {
		t.Fatal(e)
	}
	head, e := store.WriteDocument(ctx, scope, 0, "head", "head", time.Now(), sources, memory.DocumentWrite{Layer: memory.Injected, Title: "Session index", Content: "Look up verified CSV quasar import.", Links: []string{old.Ref}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("weather-%03d", i)
		_, e = store.WriteDocument(ctx, scope, 0, key, key, time.Now(), sources, memory.DocumentWrite{Layer: memory.Notes, Title: key, Content: "Weather observation about rainfall."})
		if e != nil {
			t.Fatal(e)
		}
	}
	result, e := r.ManageMemory(ctx, "review-dream", memory.Mutation{Operation: "dream", Content: "Correct the CSV quasar import rule and session index"})
	if e != nil {
		t.Fatal(e)
	}
	id := strings.TrimPrefix(result["receipt"].(string), "memory-event:")
	in, e := r.dreamInput(ctx, scope, id, time.Now(), 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	foundOld, foundHead := false, false
	for _, d := range in.Documents {
		foundOld = foundOld || d.Ref == old.Ref
		foundHead = foundHead || d.Ref == head.Ref
	}
	if !foundOld || !foundHead {
		t.Fatal("old relevant note or current injected head missing", in.Documents)
	}
	provider.reply = func(in memory.CurationInput) llm.GenerationResult {
		raw, _ := json.Marshal(memory.Proposal{Done: true, Documents: []memory.DocumentWrite{{Ref: head.Ref, Revision: head.Revision, Layer: memory.Injected, Title: head.Title, Content: "Updated index of verified CSV rules.", Links: []string{old.Ref}}}})
		return llm.GenerationResult{Text: string(raw)}
	}
	if e = r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE failed_at IS NOT NULL`) != 0 {
		t.Fatal("head update conflicted")
	}

}
func TestMemoryCompactionPreservesMixedCodeBusinessReceipt(t *testing.T) {
	_, r, _, _, _ := semanticRuntime(t)
	ctx := context.Background()
	rid := "review-run"
	page := `{"ref":"n","layer":"notes","content":"evidence","revision":1,"total_bytes":8}`
	binding := "memory-read:" + rid + ":read1"
	sealed, e := r.vault.Seal(ctx, page, binding)
	if e != nil {
		t.Fatal(e)
	}
	env, _ := json.Marshal(map[string]any{"memory_text": llm.MemoryText{Sealed: sealed, Binding: binding, EncodedBytes: llm.MemoryEncodedBytes(page)}})
	blocks, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(env)}, {"type": "text", "text": `{"sent":true,"message_id":"accepted-receipt-123"}`}})
	messages := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "code1", Name: "builtin__codemode_execute", Arguments: `{}`}}}, {Role: "tool", ToolCallID: "code1", Text: string(blocks)}}
	projected, e := r.codeMemoryProjection(ctx, agent.Run{ID: rid, Tools: []tool.Spec{memory.Definition().Spec}}, messages)
	if e != nil {
		t.Fatal(e)
	}
	wire, e := llm.ResolveMemoryMessages(ctx, llm.GenerationRequest{Messages: projected, ResolveMemory: r.memoryResolver(rid)})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(wire[1].Text, "accepted-receipt-123") {
		t.Fatal("invalid probe: live request did not show the non-memory receipt")
	}
	sources := memorySummarySources(projected)
	raw, _ := json.Marshal(sources)
	if !strings.Contains(string(raw), "accepted-receipt-123") || strings.Contains(string(raw), "sealed:v1:") {
		t.Fatal("mixed summary lost the outcome or retained ciphertext", string(raw))
	}
}

func TestMemoryDreamMaintenanceInspectionIncludesCostsAndDetail(t *testing.T) {
	s, r, store, _, p := semanticRuntime(t)
	ctx := context.Background()
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`, Usage: &llm.TokenUsage{InputTokens: 40, OutputTokens: 10}}
	}
	if _, e := r.ManageMemory(ctx, "review-metrics", memory.Mutation{Operation: "dream", Content: "Review existing notes"}); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := s.db.QueryRow("select count(*) from memory_generation where purpose='dream'").Scan(&n); e != nil || n == 0 {
		t.Fatal(n, e)
	}
	page, e := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance"}, store)
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 1 || len(page.Maintenance) != 1 || page.Maintenance[0].Operation != "dream" || page.Maintenance[0].Metrics.Generations != 1 {
		t.Fatal("Dream not visible", page)
	}
	detail, e := r.InspectMemory(ctx, memory.InspectionQuery{View: "maintenance", Ref: strings.Replace(page.Maintenance[0].Ref, "memory-event:", "memory:maintenance-", 1)}, store)
	if e != nil || len(detail.Maintenance) != 1 {
		t.Fatal(detail, e)
	}
}

func TestMemoryDreamUsesWorkerReceiptInsteadOfInaccurateSummary(t *testing.T) {
	s, r, a, p := taskFixture(t)
	ctx := context.Background()
	store, vault := evidenceMemoryVault(t, s)
	store.ConfigureLocal()
	r.ConfigureMemory(store, vault)
	const proof = "VALIDATOR_REJECTED_SEPARATOR; required encoding utf-8-no-bom"
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			if req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "I think the separator theory worked."}, nil
			}
			return calls("mcp__fixture__echo", `{"text":"`+proof+`"}`), nil
		}
		if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Task finished; summary omits the validator."}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Validate the import hypothesis"}`), nil
	}
	mustSubmit(t, a, "worker-ground-truth", "Run the import experiment")
	mustDrain(t, a)
	var run, invocation string
	if e := s.db.QueryRow(`SELECT r.run_id,i.invocation_id FROM run r JOIN tool_invocation i USING(run_id) WHERE r.worker_position='root' AND i.tool_name='mcp__fixture__echo'`).Scan(&run, &invocation); e != nil {
		t.Fatal(e)
	}
	sem := &semanticProvider{r: r, reply: func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		for i, o := range in.Observations {
			if o.Author == "tool" && strings.Contains(o.Text, proof) {
				body, _ := json.Marshal(memory.Proposal{Done: true, Documents: []memory.DocumentWrite{{Layer: memory.Notes, Title: "Verified import feedback", Content: proof, Support: []memory.SupportQuote{{Observation: i, Quote: proof}}}}})
				return llm.GenerationResult{Text: string(body)}
			}
		}
		t.Error("actual validator feedback absent from Dream")
		return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
	}}
	r.ConfigureSemanticMemory(sem)
	if _, e := r.ManageMemory(ctx, "worker-dream", memory.Mutation{Operation: "dream", Content: "Correct import theory against the actual validator"}); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	ref := "run:" + run + ":tool:" + invocation
	docs, e := store.ListDocuments(ctx, recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}, 0, memory.Notes, 20)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, d := range docs {
		for _, q := range d.Evidence {
			found = found || q.SourceRef == ref && q.Author == "tool" && q.Quote == proof
		}
	}
	if !found {
		t.Fatal("receipt-grounded note missing", docs)
	}
	read, e := r.ManageMemory(ctx, "read-worker-source", memory.Mutation{Operation: "read", Layer: memory.Transcripts, MemoryRef: ref})
	if e != nil || !strings.Contains(fmt.Sprint(read["text"]), proof) {
		t.Fatal(read, e)
	}
	owner := mustOwner(t, s)
	if e = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		_, _, _, e := r.workerMemoryOriginal(ctx, tx, "another-agent", ref, 0)
		if !errors.Is(e, recall.ErrConflict) {
			t.Error("foreign worker readable", e)
		}
		_, _, _, e = r.workerMemoryOriginal(ctx, tx, owner, ref, time.Now().Add(time.Hour).UnixMicro())
		if !errors.Is(e, recall.ErrConflict) {
			t.Error("Forget floor bypass", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT worker_offset FROM memory_dream_coverage`) != 0 {
		t.Fatal("worker coverage incomplete")
	}
	if e = r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_dream_change WHERE operation='write'`) != 1 {
		t.Fatal("covered worker replayed")
	}
}

func TestMemoryTranscriptsPageUsesExactTimeAndDoesNotLoadUnrelatedBodies(t *testing.T) {
	s, r, store, _, _ := semanticRuntime(t)
	ctx := context.Background()
	owner := mustOwner(t, s)
	instants := []string{"2026-11-08T09:00:00Z", "2026-11-08T09:00:00.000001Z", "2026-11-08T17:00:00.999999+08:00"}
	for i, stamp := range instants {
		at, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil {
			t.Fatal(e)
		}
		id := fmt.Sprintf("micro-%d", i)
		m := conversation.Message{MessageID: id, ConversationID: owner, Role: "user", Content: conversation.Text("Unicode ÉVIDENCE " + id), CreatedAt: at}
		raw, _ := json.Marshal(m)
		if _, e = s.db.Exec(`INSERT INTO conversation_intent(message_id,message_json) VALUES(?,?)`, id, string(raw)); e != nil {
			t.Fatal(e)
		}
		if got := scalar(t, s, `SELECT memory_created_at FROM conversation_intent WHERE message_id=?`, id); got != int(at.UnixMicro()) {
			t.Fatal("timestamp projection changed microseconds", got, at.UnixMicro())
		}
	}
	// A malformed older body must not poison a later page or exact ID read.
	malformed := fmt.Sprintf(`{"message_id":"old-malformed","conversation_id":%q,"role":"user","content":42,"created_at":"2020-01-01T00:00:00Z"}`, owner)
	if _, e := s.db.Exec(`INSERT INTO conversation_intent(message_id,message_json) VALUES('old-malformed',?)`, malformed); e != nil {
		t.Fatal(e)
	}
	first, e := r.InspectMemory(ctx, memory.InspectionQuery{Layer: memory.Transcripts, Limit: 1}, store)
	if e != nil || len(first.Items) != 1 || first.Items[0].Ref != "memory:transcript-micro-2" || first.Next == "" {
		t.Fatal(first, e)
	}
	second, e := r.InspectMemory(ctx, memory.InspectionQuery{Layer: memory.Transcripts, Limit: 1, Cursor: first.Next}, store)
	if e != nil || len(second.Items) != 1 || second.Items[0].Ref != "memory:transcript-micro-1" {
		t.Fatal(second, e)
	}
	detail, e := r.InspectMemory(ctx, memory.InspectionQuery{Layer: memory.Transcripts, Ref: "memory:transcript-micro-0"}, store)
	if e != nil || len(detail.Items) != 1 {
		t.Fatal(detail, e)
	}
	if _, e = r.InspectMemory(ctx, memory.InspectionQuery{Layer: memory.Transcripts, Ref: "memory:wrong-domain"}, store); !errors.Is(e, memory.ErrInspectionNotFound) {
		t.Fatal(e)
	}
}

func TestMemoryMissingFieldsSurviveNativeAndCodeModeReceipts(t *testing.T) {
	ctx := context.Background()
	const raw = `{"operation":"write","content":"PRIVATE_INPUT_NOT_A_DIAGNOSTIC"}`
	const want = "Missing required memory fields: layer, title."
	t.Run("frozen-v5", func(t *testing.T) {
		d := memory.Definition()
		d.Spec = d.PreviousSpecs[len(d.PreviousSpecs)-1]
		d.PreviousSpecs = nil
		cs := []llm.ToolCall{{ID: "legacy-write", Name: memory.ManageName, Arguments: raw}}
		s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{d}, cs)
		m, vault := evidenceMemoryVault(t, s)
		r.ConfigureMemory(m, vault)
		results, err := executor.Execute(ctx, exchange, cs)
		if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Message != "INVALID_ARGUMENT" {
			t.Fatal("changed frozen v5 error contract", results, err)
		}
	})
	t.Run("native", func(t *testing.T) {
		cs := []llm.ToolCall{{ID: "invalid-write", Name: memory.ManageName, Arguments: raw}}
		s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{memory.Definition()}, cs)
		m, vault := evidenceMemoryVault(t, s)
		r.ConfigureMemory(m, vault)
		results, err := executor.Execute(ctx, exchange, cs)
		if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Message != want {
			t.Fatal(results, err)
		}
		var saved string
		if err = s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, memory.ManageName).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(saved, want) || strings.Contains(saved, "PRIVATE_INPUT_NOT_A_DIAGNOSTIC") {
			t.Fatal(saved)
		}
	})
	t.Run("codemode", func(t *testing.T) {
		s, r, a, p := childFixture(t, codemode.Definition(), memory.Definition())
		m, vault := evidenceMemoryVault(t, s)
		r.ConfigureMemory(m, vault)
		r.ConfigureJavaScript(realCodeRuntime(t))
		p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
			if req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "Observed validation rejection"}, nil
			}
			source := `try { await tools.builtin__memory_manage(` + raw + `); } catch(e) { text(e.message); }`
			args, _ := json.Marshal(map[string]string{"source": source})
			return calls(codemode.ToolName, string(args)), nil
		})
		mustSubmit(t, a, "invalid-code-write", "Try the memory control")
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := a.Drain(bounded); err != nil {
			t.Fatal(err)
		}
		var saved string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=? AND parent_invocation_id IS NOT NULL`, memory.ManageName).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(saved, want) || strings.Contains(saved, "PRIVATE_INPUT_NOT_A_DIAGNOSTIC") {
			t.Fatal(saved)
		}
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(saved, want) || strings.Contains(saved, "PRIVATE_INPUT_NOT_A_DIAGNOSTIC") {
			t.Fatal("CodeMode hid the Host diagnostic", saved)
		}
	})
}

func TestMemoryDreamDoesNotBlindlyRecreateOversizedInjectedHead(t *testing.T) {
	s, r, store, _, _ := semanticRuntime(t)
	ctx := context.Background()
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	head, e := store.WriteDocument(ctx, scope, 0, "large-head", "large-head", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:head"}}, memory.DocumentWrite{Layer: memory.Injected, Title: "Existing index", Content: strings.Repeat("Original index entry. ", 1800)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE model_profile SET context_window=8192,max_output_tokens=1024,version=version+1,updated_at=updated_at+1`); e != nil {
		t.Fatal(e)
	}
	result, e := r.ManageMemory(ctx, "large-head-review", memory.Mutation{Operation: "dream", Content: "Review the current index"})
	if e != nil {
		t.Fatal(e)
	}
	id := strings.TrimPrefix(result["receipt"].(string), "memory-event:")
	if _, e = r.dreamInput(ctx, scope, id, time.Now(), 0, 0); !errors.Is(e, llm.ContextLengthExceeded) {
		t.Fatal("head silently omitted instead of failing budget check", e)
	}
	saved, e := store.ReadDocument(ctx, scope, 0, head.Ref)
	if e != nil || saved.Revision != head.Revision {
		t.Fatal("existing head changed", saved, e)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_generation`) != 0 {
		t.Fatal("model was called without the current head")
	}
}

func TestMemoryWorkerEvidencePageBoundaryAndContinuation(t *testing.T) {
	for _, count := range []int{63, 64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, r, a, p := taskFixture(t)
			ctx := context.Background()
			store, vault := evidenceMemoryVault(t, s)
			store.ConfigureLocal()
			r.ConfigureMemory(store, vault)
			if _, e := s.db.Exec(`UPDATE model_profile SET context_window=65536,version=version+1,updated_at=updated_at+1`); e != nil {
				t.Fatal(e)
			}
			emitted := 0
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if isRoot(req) {
					if emitted == count {
						return llm.GenerationResult{Text: "Receipts collected"}, nil
					}
					result := llm.GenerationResult{}
					end := min(count, emitted+32)
					for i := emitted; i < end; i++ {
						args, _ := json.Marshal(map[string]string{"text": fmt.Sprintf("receipt-%02d", i)})
						result.ToolCalls = append(result.ToolCalls, llm.ToolCall{ID: fmt.Sprintf("receipt-%02d", i), Name: "mcp__fixture__echo", Arguments: string(args)})
					}
					emitted = end
					return result, nil
				}
				if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "Observed completion"}, nil
				}
				return calls(task.ManageName, `{"operation":"create","task":"Collect original receipts"}`), nil
			}
			mustSubmit(t, a, "worker-page-boundary", "Collect receipts")
			mustDrain(t, a)
			var status string
			if e := s.db.QueryRow(`SELECT status FROM run WHERE worker_position='root'`).Scan(&status); e != nil || status != "completed" && status != "failed" {
				t.Fatal(status, e)
			}
			if n := scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=(SELECT run_id FROM run WHERE worker_position='root')`); n != count {
				t.Fatal("missing accepted receipts", n, count)
			}
			scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
			in := memory.CurationInput{}
			if e := r.dreamWorkerEvidence(ctx, scope, &in, time.Now(), 0, 1<<20); e != nil {
				t.Fatal(e)
			}
			if len(in.Observations) != min(count, 64)+1 {
				t.Fatal("missing original task/receipts", len(in.Observations))
			}
			if count <= 64 {
				if in.WorkerOffset != 0 {
					t.Fatal("completed page incorrectly schedules another Dream", in.WorkerOffset)
				}
				return
			}
			if in.WorkerOffset != 65 {
				t.Fatal("wrong next source selector", in.WorkerOffset)
			}
			if _, e := s.db.Exec(`INSERT INTO memory_dream_coverage(agent_id,through_sequence,next_offset,worker_sequence,worker_offset) VALUES(?,0,0,?,?)`, scope.AgentID, in.WorkerThrough, in.WorkerOffset); e != nil {
				t.Fatal(e)
			}
			next := memory.CurationInput{}
			if e := r.dreamWorkerEvidence(ctx, scope, &next, time.Now(), 0, 1<<20); e != nil {
				t.Fatal(e)
			}
			if len(next.Observations) != 1 || next.WorkerOffset != 0 || next.WorkerThrough != in.WorkerThrough {
				t.Fatal("last receipt not completed", next)
			}
			for _, o := range in.Observations {
				if o.Source.Ref == next.Observations[0].Source.Ref {
					t.Fatal("receipt repeated across page boundary")
				}
			}
		})
	}
}
