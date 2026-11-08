package sqlite

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
	"testing"
	"time"
)

func TestLayeredMemoryNavigationSelectsScopedLinksWithoutRewritingSources(t *testing.T) {
	s, r, _, _, _ := loopFixture(t)
	store, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(store, vault)
	ctx := context.Background()
	scope := recall.Scope{UserID: "local", AgentID: mustOwner(t, s)}
	docs := []memory.Document{}
	for i, topic := range []string{"Orchid travel", "Orchid invoice", "Other weather"} {
		d, err := store.WriteDocument(ctx, scope, 0, topic, topic, time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:test"}}, memory.DocumentWrite{Layer: memory.Representations, Title: topic, Content: topic + " original qualifiers retained"})
		if err != nil {
			t.Fatal(i, err)
		}
		docs = append(docs, d)
	}
	foreign, err := store.WriteDocument(ctx, recall.Scope{UserID: "other", AgentID: scope.AgentID}, 0, "foreign", "foreign", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:foreign"}}, memory.DocumentWrite{Layer: memory.Representations, Title: "Orchid invoice private", Content: "FOREIGN_SECRET_MARKER"})
	if err != nil {
		t.Fatal(err)
	}
	index := memory.Document{Ref: "memory:index", Revision: 2, Content: strings.Repeat("用户约束不变。", 600), Links: []string{docs[0].Ref, docs[1].Ref, docs[2].Ref, foreign.Ref}}
	messages := []llm.Message{{Role: "user", Text: strings.Repeat("background instructions ", 35) + "Orchid invoice"}}
	text, err := r.memoryNavigation(ctx, store, scope, 0, index, messages, 2048)
	if err != nil || !strings.Contains(text, docs[1].Ref) || strings.Contains(text, foreign.Ref) || strings.Contains(text, "FOREIGN_SECRET_MARKER") || len(text) > 2048 || !strings.Contains(text, "read the index") {
		t.Fatal(text, err)
	}
	if !strings.HasSuffix(index.Content, "用户约束不变。") || len(index.Links) != 4 {
		t.Fatal("source document mutated")
	}
}
func TestRecallVisibleResultsRequireOwnedReceiptAndCompleteVisibleFrame(t *testing.T) {
	ctx := context.Background()
	call := llm.ToolCall{ID: "search", Name: recall.ToolName, Arguments: `{"query":"orchid","limit":4}`}
	_, r, c, executor, exchange := admitPolicy(t, []tool.Definition{recall.Definition(recall.UnconfiguredIdentity)}, []llm.ToolCall{call})
	probe := &explicitRecallProbe{hits: []recall.Hit{{SourceRef: "conversation:orchid", SourceType: "conversation", Content: "Orchid original decision", OccurredAt: time.Now()}}}
	r.ConfigureRecall(recall.Service{Memory: probe, Conversation: probe})
	results, err := executor.Execute(ctx, exchange, []llm.ToolCall{call})
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	blocks, _ := json.Marshal([]map[string]any{{"type": "text", "text": results[0].Text, "console": true}})
	for _, raw := range []string{results[0].Text, string(blocks)} {
		refs, err := r.visibleRecallRefs(ctx, run, []llm.Message{{Role: "tool", ToolCallID: call.ID, Text: raw}})
		if err != nil || len(refs) != 1 || refs[0] != "conversation:orchid" {
			t.Fatal(refs, err)
		}
	}
	for _, messages := range [][]llm.Message{nil, {{Role: "tool", ToolCallID: call.ID, Text: ""}}, {{Role: "tool", ToolCallID: "unrelated", Text: results[0].Text}}, {{Role: "tool", ToolCallID: call.ID, Text: strings.Replace(results[0].Text, "original decision", "forged", 1)}}} {
		refs, err := r.visibleRecallRefs(ctx, run, messages)
		if err != nil || len(refs) != 0 {
			t.Fatal("unseen/unowned evidence excluded", refs, err)
		}
	}
	if completeRecallHit(recall.Hit{SourceRef: "memory:x", SourceType: "memory", Content: "part", TotalBytes: 100}) {
		t.Fatal("truncated evidence marked complete")
	}

	// A real child receipt is correlated through its provider-visible parent,
	// not merely through the child call ID used by the nested executor.
	_, err = r.host.db.ExecContext(ctx, `INSERT INTO tool_invocation(invocation_id,run_id,parent_invocation_id,provider_exchange_id,provider_call_id,tool_name,arguments_json,request_hash,replay_policy,idempotency_key,status,wait_reason,attempt_count,delivery_seq,created_at,updated_at,version,desired_state,policy_json,conflict_keys) SELECT 'visible-parent',run_id,NULL,provider_exchange_id,'wrapper','builtin__codemode_execute','{}',request_hash,replay_policy,'visible-parent-key','succeeded',NULL,attempt_count,delivery_seq,created_at,updated_at,version,desired_state,policy_json,conflict_keys FROM tool_invocation WHERE run_id=? AND provider_call_id='search'`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.host.db.ExecContext(ctx, `INSERT INTO tool_invocation(invocation_id,run_id,parent_invocation_id,provider_exchange_id,provider_call_id,tool_name,arguments_json,request_hash,replay_policy,idempotency_key,status,wait_reason,attempt_count,delivery_seq,result_json,result_ready_at,created_at,updated_at,version,desired_state,policy_json,conflict_keys) SELECT 'visible-child',run_id,'visible-parent',provider_exchange_id,'inner',tool_name,arguments_json,request_hash,replay_policy,'visible-child-key',status,wait_reason,attempt_count,delivery_seq,result_json,result_ready_at,created_at,updated_at,version,desired_state,policy_json,conflict_keys FROM tool_invocation WHERE run_id=? AND provider_call_id='search'`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := r.visibleRecallRefs(ctx, run, []llm.Message{{Role: "tool", ToolCallID: "wrapper", Text: string(blocks)}})
	if err != nil || len(refs) != 1 {
		t.Fatal("visible parent lost child evidence", refs, err)
	}
	refs, err = r.visibleRecallRefs(ctx, run, []llm.Message{{Role: "tool", ToolCallID: "wrapper", Text: ""}})
	if err != nil || len(refs) != 0 {
		t.Fatal("failed parent hid successful child", refs, err)
	}
	// Compaction removes the visible frame; the original becomes retrievable again.
	if refs, err := r.visibleRecallRefs(ctx, agent.Run{ID: run.ID}, nil); err != nil || len(refs) != 0 {
		t.Fatal(refs, err)
	}
}
