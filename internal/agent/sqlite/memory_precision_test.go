package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestLayeredMemoryTranscriptReadIsScopedPagedSealedAndRevoked(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "owned", Name: memory.ManageName, Arguments: `{"operation":"read","layer":"transcripts","memory_ref":"conversation:source-one","offset":5,"limit":64}`}, {ID: "foreign", Name: memory.ManageName, Arguments: `{"operation":"read","layer":"transcripts","memory_ref":"conversation:source-foreign"}`}}
	s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{memory.Definition()}, calls)
	m, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(m, vault)
	owner := mustOwner(t, s)
	binder := r.history.(interface {
		BindConversation(context.Context, string, recall.Scope) error
	})
	for _, seed := range []struct{ id, channel, user string }{{"source-one", "channel-one", "local"}, {"source-foreign", "channel-foreign", "other-user"}} {
		if err := binder.BindConversation(ctx, seed.channel, recall.Scope{UserID: seed.user, AgentID: owner}); err != nil {
			t.Fatal(err)
		}
		if err := r.history.Append(ctx, []conversation.Message{{MessageID: seed.id, ConversationID: seed.channel, Role: "user", Content: conversation.Text("head PRIVATE_ORIGINAL_EVIDENCE " + strings.Repeat("tail ", 50)), CreatedAt: time.Now(), Source: "test"}}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 2 || results[0].Error != nil || results[1].Error == nil || results[1].Error.Code != "NOT_FOUND" {
		t.Fatal(results, err)
	}
	if strings.Contains(results[0].Text, "PRIVATE_ORIGINAL_EVIDENCE") {
		t.Fatal("plaintext persisted in Tool result")
	}
	var frame struct {
		Memory llm.MemoryText `json:"memory_text"`
	}
	if json.Unmarshal([]byte(results[0].Text), &frame) != nil {
		t.Fatal(results[0])
	}
	plain, err := vault.Open(ctx, frame.Memory.Sealed, frame.Memory.Binding)
	if err != nil || !strings.Contains(plain, "PRIVATE_ORIGINAL_EVIDENCE") || !strings.Contains(plain, "next_offset") {
		t.Fatal(plain, err)
	}
	if _, err := r.ManageMemory(ctx, "forget-history", memory.Mutation{Operation: "forget", Category: "all"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ManageMemory(ctx, "read-after-forget", memory.Mutation{Operation: "read", Layer: memory.Transcripts, MemoryRef: "conversation:source-one"}); err == nil {
		t.Fatal("Forget exposed an original transcript")
	}
}

func TestLayeredMemoryCodeReadProjectionIsStableScopedAndDoesNotRewriteReceipt(t *testing.T) {
	s, r, _, _, _ := loopFixture(t)
	m, v := evidenceMemoryVault(t, s)
	r.ConfigureMemory(m, v)
	ctx := context.Background()
	plain := `{"text":"PRIVATE_TRANSPORT_EVIDENCE"}`
	binding := "memory-read:run:call"
	sealed, err := v.Seal(ctx, plain, binding)
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]llm.MemoryText{"memory_text": {Sealed: sealed, Binding: binding, EncodedBytes: llm.MemoryEncodedBytes(plain)}})
	raw, _ := json.Marshal([]map[string]string{{"type": "text", "text": string(envelope)}})
	original := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "code", Name: "builtin__codemode_execute"}}}, {Role: "tool", ToolCallID: "code", Text: string(raw)}}
	run := agent.Run{ID: "run", Tools: []tool.Spec{memory.Definition().Spec}}
	first, err := r.codeMemoryProjection(ctx, run, original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.codeMemoryProjection(ctx, run, original)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if err != nil || string(a) != string(b) || strings.Contains(string(a), "PRIVATE_TRANSPORT_EVIDENCE") || original[1].Text != string(raw) || first[1].Memory == nil {
		t.Fatal("unstable/unsafe continuation", string(a), err)
	}
	req := llm.GenerationRequest{Messages: first, ResolveMemory: r.memoryResolver(run.ID)}
	wire, err := llm.ResolveMemoryMessages(ctx, req)
	if err != nil || !strings.Contains(wire[1].Text, "PRIVATE_TRANSPORT_EVIDENCE") {
		t.Fatal(wire, err)
	}
	req.ResolveMemory = r.memoryResolver("other-run")
	if _, err := llm.ResolveMemoryMessages(ctx, req); err == nil {
		t.Fatal("cross-run envelope accepted")
	}
	run.Tools[0].Version = 3
	legacy, err := r.codeMemoryProjection(ctx, run, original)
	if err != nil || legacy[1].Memory != nil || legacy[1].Text != string(raw) {
		t.Fatal("old frozen tool protocol changed", err)
	}
}

func TestLayeredMemoryStaleInjectedBypassesCoalescingDelay(t *testing.T) {
	s, r, _, a, _ := semanticRuntime(t)
	ctx := context.Background()
	note, err := r.ManageMemory(ctx, "seed-note-refresh", memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Current plan", Content: "Plan version one"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ManageMemory(ctx, "seed-injected-refresh", memory.Mutation{Operation: "write", Layer: memory.Injected, Title: "Context", Content: "Use the current plan", Links: []string{note["ref"].(string)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ManageMemory(ctx, "correct-plan", memory.Mutation{Operation: "write", Layer: memory.Notes, MemoryRef: note["ref"].(string), Revision: 1, Title: "Current plan", Content: "Plan version two"})
	if err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "recent-correction", "The current plan changed.")
	queued, err := r.scheduleDream(ctx)
	if err != nil || !queued || scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE source_type='dream'`) != 1 {
		t.Fatal("stale summary waited for eight messages or five minutes", queued, err)
	}
	if queued, err = r.scheduleDream(ctx); err != nil || queued {
		t.Fatal("repeated freshness check queued duplicate work", queued, err)
	}
}
