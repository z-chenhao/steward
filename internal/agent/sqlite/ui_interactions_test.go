package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

const removeLayeredMemoryFixtureSchema = `DROP TRIGGER IF EXISTS conversation_memory_time; DROP TRIGGER IF EXISTS conversation_memory_time_immutable; DROP INDEX IF EXISTS conversation_memory_page; ALTER TABLE conversation_intent DROP COLUMN memory_created_at; DROP TABLE IF EXISTS memory_access; DROP TABLE IF EXISTS memory_injection_access; DROP TABLE IF EXISTS memory_dream_change; DROP TABLE IF EXISTS runtime_owner; DROP TABLE IF EXISTS run_injected_memory; DROP TABLE IF EXISTS memory_dream_step; DROP TABLE IF EXISTS memory_dream_coverage; `

const removeUIInteractionFixtureSchema = removeLayeredMemoryFixtureSchema + `DROP TABLE IF EXISTS generation_cost_estimate; DROP TABLE IF EXISTS memory_cost_reservation; DROP TABLE IF EXISTS memory_generation; DROP TABLE IF EXISTS memory_semantic_receipt; DROP TABLE IF EXISTS connected_apps; DROP TABLE IF EXISTS ui_action_request; DROP TABLE IF EXISTS main_ui_context; DROP TABLE IF EXISTS ui_view_state; `

func uiInteractionFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider, string, *atomic.Int32) {
	t.Helper()
	counter := &atomic.Int32{}
	definition := tool.Definition{Spec: tool.Spec{Name: "mcp__ui__refresh", Version: 1, Permission: "read_only", ReplayPolicy: "safe", InputSchema: `{"type":"object","properties":{"filter":{"type":"string"}},"required":["filter"],"additionalProperties":false}`}, Execute: func(context.Context, string) tool.Result {
		counter.Add(1)
		return tool.Result{Text: `{"rows":[{"value":42}]}`}
	}}
	s, r, a, p := childFixture(t, ui.ShowDefinition(), definition)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Done."}, nil
		}
		for _, m := range req.Messages {
			if strings.Contains(m.Text, "Runtime ui_action_request") {
				return calls("mcp__ui__refresh", `{"filter":"A"}`), nil
			}
		}
		doc := `{"title":"Explore","fallback":"A is 42.","components":[{"name":"Table","props":{"title":"Values","columns":["Option","Value"],"rows":[["A","42"]],"source":"User data"}},{"name":"steward.action","props":{"id":"refresh","label":"Refresh values","tool":"mcp__ui__refresh","arguments":{"filter":"A"},"bindings":[{"argument":"filter","component":0,"path":"/filter"}]}}]}`
		return calls(ui.ShowName, doc), nil
	}
	mustSubmit(t, a, "show-interaction", "Show values")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindChannel(context.Background(), "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	return s, r, a, p, ref, counter
}
func TestUIStateCASFreezeAndActionUsesOrdinaryExecutor(t *testing.T) {
	ctx := context.Background()
	s, r, a, p, ref, counter := uiInteractionFixture(t)
	empty, err := s.WebUIState(ctx, ref+"#0")
	if err != nil || empty.Version != 0 {
		t.Fatal(empty, err)
	}
	state := ui.ViewState{State: json.RawMessage(`{"filter":"A"}`), ModelContext: json.RawMessage(`{"selected":"A"}`)}
	saved, err := s.SaveWebUIState(ctx, ref+"#0", state)
	if err != nil || saved.Version != 1 {
		t.Fatal(saved, err)
	}
	if _, err = s.SaveWebUIState(ctx, ref+"#0", state); err != conversation.ErrConflict {
		t.Fatal("stale writer overwrote view", err)
	}
	if _, err = s.SaveWebUIState(ctx, "ui:outside#0", state); err != agent.ErrNotFound {
		t.Fatal("foreign state accepted", err)
	}
	source := "ui-state-action-identity"
	receipt, err := r.SubmitUIAction(ctx, ref+"#1", source)
	if err != nil {
		t.Fatal(err)
	}
	next := saved
	next.State = json.RawMessage(`{"filter":"B"}`)
	next.ModelContext = json.RawMessage(`{"selected":"B"}`)
	if _, err = s.SaveWebUIState(ctx, ref+"#0", next); err != nil {
		t.Fatal(err)
	}
	again, err := r.SubmitUIAction(ctx, ref+"#1", source)
	if err != nil || again.MessageID != receipt.MessageID {
		t.Fatal("retry changed request", again, err)
	}
	pending, err := s.WebUIActionResult(ctx, ref+"#1", source)
	if err != nil || pending.Status != "pending" {
		t.Fatal(pending, err)
	}
	mustDrain(t, a)
	result, err := s.WebUIActionResult(ctx, ref+"#1", source)
	if err != nil || result.Status != "succeeded" || result.Result == nil || !strings.Contains(result.Result.Text, "42") || counter.Load() != 1 {
		t.Fatal(result, err, counter.Load())
	}
	var raw string
	if err = s.db.QueryRow(`SELECT context_json FROM main_ui_context WHERE message_id=?`, receipt.MessageID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `\"selected\":\"A\"`) || strings.Contains(raw, `\"selected\":\"B\"`) {
		t.Fatal("UI context was not frozen at input acceptance", raw)
	}
	if !strings.Contains(raw, "Persisted component state") || !strings.Contains(raw, "not the active page") {
		t.Fatal("persisted state can be mistaken for the current topic", raw)
	}
	found := false
	for _, req := range p.requests {
		for _, m := range req.Messages {
			if strings.Contains(m.Text, "Runtime ui_action_request") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("structured action did not reach Main")
	}
	if _, err = r.SubmitUIAction(ctx, ref+"#0", source); err == nil {
		t.Fatal("non-action accepted")
	}
	restored, err := s.WebUIState(ctx, ref+"#0")
	if err != nil || restored.Version != 2 || !strings.Contains(string(restored.State), "B") {
		t.Fatal(restored, err)
	}
	bound, err := r.SubmitUIAction(ctx, ref+"#1", "ui-state-second-action")
	if err != nil {
		t.Fatal(err)
	}
	var arguments string
	if err = s.db.QueryRow(`SELECT arguments_json FROM ui_action_request WHERE message_id=?`, bound.MessageID).Scan(&arguments); err != nil || arguments != `{"filter":"B"}` {
		t.Fatal("binding did not use current component state", arguments, err)
	}
	// A normal user turn receives the current explicit state without technical
	// metadata appearing in its visible Conversation content.
	user, err := r.AcceptInbound(ctx, interaction.InboundEvent{Channel: "web", Subject: "browser", Destination: "local", EventID: "state-context", SourceID: "state-context", Content: conversation.Text("Compare my selection")})
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM main_ui_context WHERE message_id=?`, user.MessageID) != 1 {
		t.Fatal("ordinary input lost UI evidence")
	}
}
func TestUIStateConcurrentWritersAndInvalidPayloads(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, ref, _ := uiInteractionFixture(t)
	var accepted atomic.Int32
	var wait sync.WaitGroup
	for range 6 {
		wait.Go(func() {
			_, err := s.SaveWebUIState(ctx, ref+"#0", ui.ViewState{State: json.RawMessage(`{"n":1}`), ModelContext: json.RawMessage(`{}`)})
			if err == nil {
				accepted.Add(1)
			} else if err != conversation.ErrConflict {
				t.Errorf("writer: %v", err)
			}
		})
	}
	wait.Wait()
	if accepted.Load() != 1 {
		t.Fatal("concurrent writers overwrote each other", accepted.Load())
	}
	for _, raw := range []string{`[]`, `null`, `{"__proto__":{}}`, `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":1}}}}}}}}}`, `{"value":"` + strings.Repeat("x", 17000) + `"}`} {
		if _, err := s.SaveWebUIState(ctx, ref+"#0", ui.ViewState{Version: 1, State: json.RawMessage(raw), ModelContext: json.RawMessage(`{}`)}); err != conversation.ErrInvalid {
			t.Fatal("invalid state accepted", raw[:min(40, len(raw))], err)
		}
	}
}

func TestUIActionCannotApproveAnExternalEffect(t *testing.T) {
	ctx := context.Background()
	var invoked atomic.Int32
	definition := tool.Definition{Spec: tool.Spec{Name: "mcp__ui__publish", Version: 1, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 1000, OutputBytes: 4096, Provider: "fixture", ConflictPolicy: "none", InputSchema: `{"type":"object","properties":{},"additionalProperties":false}`}, Invoke: func(context.Context, tool.Request) tool.Outcome {
		invoked.Add(1)
		return tool.Outcome{Result: tool.Result{Text: "Published"}}
	}}
	s, r, a, p := childFixture(t, ui.ShowDefinition(), definition)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Ready."}, nil
		}
		for _, m := range req.Messages {
			if strings.Contains(m.Text, "Runtime ui_action_request") {
				return calls("mcp__ui__publish", `{}`), nil
			}
		}
		return calls(ui.ShowName, `{"title":"Report","fallback":"Report ready.","components":[{"name":"steward.action","props":{"id":"publish","label":"Publish report","tool":"mcp__ui__publish","arguments":{}}}]}`), nil
	}
	mustSubmit(t, a, "publish-ui", "Show report")
	mustDrain(t, a)
	if _, err := s.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SubmitUIAction(ctx, ref+"#0", "external-action-source"); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if invoked.Load() != 0 || scalar(t, s, `SELECT count(*) FROM approval WHERE status='pending'`) != 1 {
		t.Fatal("UI click bypassed external approval", invoked.Load())
	}
	result, err := s.WebUIActionResult(ctx, ref+"#0", "external-action-source")
	if err != nil || result.Status != "waiting" && result.Status != "pending" {
		t.Fatal(result, err)
	}
}
