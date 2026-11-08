package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestUICorrectionRecoversAfterReceiptCommitFailure(t *testing.T) {
	s, runs, a, p := childFixture(t, ui.ShowDefinition())
	var step atomic.Int32
	var oldRef string
	doc := `{"title":"Original","fallback":"Original","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		switch step.Add(1) {
		case 1:
			return calls(ui.ShowName, doc), nil
		case 2:
			if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&oldRef); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`CREATE TRIGGER correction_fault BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND json_extract(NEW.arguments_json,'$.replace') IS NOT NULL BEGIN SELECT RAISE(ABORT,'correction receipt fault'); END`); err != nil {
				t.Fatal(err)
			}
			return calls(ui.ShowName, strings.Replace(doc, `"title":"Original"`, `"replace":"`+oldRef+`","title":"Corrected"`, 1)), nil
		default:
			return llm.GenerationResult{Text: "Corrected."}, nil
		}
	}
	mustSubmit(t, a, "receipt-fault", "Correct before delivery")
	if err := a.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt fault not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 2 {
		t.Fatal("correction staging lost")
	}
	var runID string
	if err := s.db.QueryRow(`SELECT run_id FROM ui_widget LIMIT 1`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	claim, err := runs.Claim(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := runs.StagedUIPreviews(context.Background(), claim)
	if err != nil || len(staged) != 1 || !strings.Contains(staged[0].Arguments, `"title":"Original"`) {
		t.Fatal("uncommitted correction hid its successful target", staged, err)
	}
	if err = runs.Release(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER correction_fault`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 2 || scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 2 {
		t.Fatal("recovery duplicated or lost correction")
	}
}

func TestUICorrectionSelectsOnePendingTargetAndPreservesHistory(t *testing.T) {
	s, _, a, p := childFixture(t, ui.ShowDefinition())
	var step atomic.Int32
	var oldRef string
	doc := func(title, replace string) string {
		b, _ := json.Marshal(ui.Widget{Title: title, Fallback: title, Replace: replace, Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}`)}}})
		return string(b)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		switch step.Add(1) {
		case 1:
			return calls(ui.ShowName, doc("Original", "")), nil
		case 2:
			if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget LIMIT 1`).Scan(&oldRef); err != nil {
				t.Fatal(err)
			}
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "correct-a", Name: ui.ShowName, Arguments: doc("Correct A", oldRef)}, {ID: "correct-b", Name: ui.ShowName, Arguments: doc("Correct B", oldRef)}}}, nil
		case 3:
			return calls(ui.ShowName, doc("Distinct section", "")), nil
		default:
			return llm.GenerationResult{Text: "The corrected answer and a distinct section."}, nil
		}
	}
	mustSubmit(t, a, "correct-pending", "Correct a pending visual")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 3 || scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 2 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE error_json LIKE '%NOT_FOUND%'`) != 1 {
		rows, _ := s.db.Query(`SELECT tool_name,status,coalesce(error_json,''),arguments_json FROM tool_invocation`)
		defer rows.Close()
		for rows.Next() {
			var name, status, failure, args string
			_ = rows.Scan(&name, &status, &failure, &args)
			t.Log(name, status, failure, args)
		}
		t.Fatalf("corrections branched or lost a distinct visual: widgets=%d bound=%d conflicts=%d", scalar(t, s, `SELECT count(*) FROM ui_widget`), scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`), scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE error_json LIKE '%NOT_FOUND%'`))
	}
	var original, message string
	if err := s.db.QueryRow(`SELECT document_json FROM ui_widget WHERE ui_ref=? AND message_id IS NULL`, oldRef).Scan(&original); err != nil || !strings.Contains(original, "Original") {
		t.Fatal("original evidence changed", err)
	}
	if err := s.db.QueryRow(`SELECT message_id FROM ui_widget WHERE message_id IS NOT NULL LIMIT 1`).Scan(&message); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT message_json FROM conversation_intent WHERE message_id=?`, message).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m conversation.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	projection, err := s.WebProjection(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err := json.Unmarshal(projection, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Components) == 0 || strings.Contains(spec.Fallback, "Original") {
		t.Fatal("superseded fallback delivered", spec)
	}
	// A fresh Main input cannot correct an earlier delivered artifact.
	var delivered string
	if err = s.db.QueryRow(`SELECT ui_ref FROM ui_widget WHERE message_id IS NOT NULL LIMIT 1`).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	step.Store(0)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if step.Add(1) == 1 {
			return calls(ui.ShowName, doc("Forbidden", delivered)), nil
		}
		return llm.GenerationResult{Text: "History preserved."}, nil
	}
	mustSubmit(t, a, "correct-delivered", "Try changing history")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 3 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE error_json LIKE '%NOT_FOUND%'`) != 2 {
		t.Fatal("delivered artifact was replaced")
	}
}

func TestUIEmptyExpressionsPersistCanonicalArtifactAndRejectNonemptyString(t *testing.T) {
	for _, expressions := range []string{`""`, `"initialize()"`} {
		t.Run(expressions, func(t *testing.T) {
			raw := `{"title":"Roadmap","fallback":"Interview customers, then build an MVP.","components":[{"name":"generateSandboxedUi","props":{"initialHeight":460,"placeholderMessages":[],"css":"","html":"<p>Week 1–2: interview customers</p>","jsFunctions":"","jsExpressions":` + expressions + `}}]}`
			calls := []llm.ToolCall{{ID: "roadmap", Name: ui.ShowName, Arguments: raw}}
			s, _, _, executor, exchange := admitPolicy(t, []tool.Definition{ui.ShowDefinition()}, calls)
			results, err := executor.Execute(context.Background(), exchange, calls)
			if err != nil || len(results) != 1 {
				t.Fatal(results, err)
			}
			if expressions != `""` {
				failure := results[0].Error
				if failure == nil || failure.Code != "INVALID_ARGUMENT" || scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 {
					t.Fatal("invalid script string was accepted", failure)
				}
				return
			}
			if results[0].Error != nil {
				t.Fatal(results[0].Error)
			}
			var document, accepted string
			if err := s.db.QueryRow(`SELECT w.document_json,i.arguments_json FROM ui_widget w JOIN tool_invocation i USING(invocation_id)`).Scan(&document, &accepted); err != nil {
				t.Fatal(err)
			}
			if accepted != raw || !strings.Contains(document, `"jsExpressions":[]`) {
				t.Fatal("normalized artifact changed the accepted invocation", document, accepted)
			}
			widget, ok := ui.StoredWidget(document)
			if _, valid := ui.Sandbox(widget); !ok || !valid {
				t.Fatal("canonical artifact cannot be replayed")
			}
		})
	}
}

func TestMainUIWidgetUsesCodeModeAndDurableReply(t *testing.T) {
	s, r, a, p := childFixture(t, ui.ShowDefinition(), codemode.Definition())
	r.ConfigureJavaScript(realCodeRuntime(t))
	var previewed atomic.Bool
	var committed atomic.Bool
	a.Sink = func(e agent.Event) {
		if e.Kind == "ui_preview_update" && e.UIPreview != nil && len(e.UIPreview.Components) > 0 {
			previewed.Store(true)
		}
		if e.Kind == "ui_preview_committed" {
			committed.Store(true)
		}
	}
	defs := []tool.Definition{}
	for _, spec := range r.registry.Describe() {
		d, _ := r.registry.Resolve(spec.Name)
		d.Spec.CodeModeOnly = spec.Name != codemode.ToolName
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	for _, spec := range agent.WorkerTools(registry.Describe()) {
		if spec.Name == ui.ShowName {
			t.Fatal("Worker can present UI")
		}
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if len(req.Tools) != 1 || req.Tools[0].Name != codemode.ToolName {
			t.Fatal("UI became a native model tool", req.Tools)
		}
		count := 0
		for _, m := range req.Messages {
			if m.Role == "system" {
				count++
			}
		}
		if count != 1 {
			t.Fatal("Agent used split system messages")
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			if !previewed.Load() {
				t.Error("Code Mode UI was withheld until final reply")
			}
			return llm.GenerationResult{Text: "Explore the comparison."}, nil
		}
		doc, _ := json.Marshal(ui.Widget{Title: "Comparison", Fallback: "The result is 42.", Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{"title":"Result","columns":["Value"],"rows":[["42"]],"source":"Calculated fixture"}`)}}})
		args, _ := json.Marshal(map[string]string{"source": "return await tools.builtin__intelligent_ui_show(" + string(doc) + ");"})
		return calls(codemode.ToolName, string(args)), nil
	}
	mustSubmit(t, a, "ui-widget", "Show the useful answer")
	mustDrain(t, a)
	if !previewed.Load() || !committed.Load() {
		t.Fatal("preview lifecycle missing", previewed.Load(), committed.Load())
	}
	if scalar(t, s, `SELECT count(*) FROM run_trace WHERE json_extract(event_json,'$.kind') LIKE 'ui_preview_%'`) != 0 {
		t.Fatal("preview persisted into execution trace")
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__intelligent_ui_show' AND status='succeeded' AND attempt_count=1`) != 1 {
		t.Fatal("widget missing or duplicated")
	}
	var ref, messageID string
	if err = s.db.QueryRow(`SELECT ui_ref,message_id FROM ui_widget`).Scan(&ref, &messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebWidget(context.Background(), ref); err != agent.ErrNotFound {
		t.Fatal("native component exposed as sandbox", err)
	}
	var raw string
	if err = s.db.QueryRow(`SELECT message_json FROM conversation_intent WHERE message_id=?`, messageID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m conversation.Message
	if json.Unmarshal([]byte(raw), &m) != nil {
		t.Fatal(raw)
	}
	projection, err := s.WebProjection(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if json.Unmarshal(projection, &spec) != nil {
		t.Fatal(string(projection))
	}
	found := false
	for _, c := range spec.Components {
		found = found || c.Type == "openintelligentui" && c.Name == "Table"
	}
	if !found {
		t.Fatal("reply lost UI reference", spec)
	}
	bubbles, err := s.WebBubbles(context.Background(), m, spec)
	if err != nil || len(bubbles) != 2 || len(bubbles[0].Components) != 1 || bubbles[0].Components[0].Name != "Table" || bubbles[1].Fallback != "Explore the comparison." {
		t.Fatal("UI must precede the separate final reply", bubbles, err)
	}
	again, err := s.WebProjection(context.Background(), m)
	if err != nil || string(again) != string(projection) {
		t.Fatal("Web bubble grouping changed the frozen delivery", err)
	}
	if _, err = s.WebWidget(context.Background(), "ui:outside"); err != agent.ErrNotFound {
		t.Fatal("out-of-scope UI accessible", err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 {
		t.Fatal("settled UI repeated")
	}
}

func TestUIStagingRecoversAfterResultCommitFailure(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "show", Name: ui.ShowName, Arguments: `{"title":"Answer","fallback":"The answer is 42.","components":[{"name":"Table","props":{"title":"Answer","columns":["Value"],"rows":[["42"]],"source":"Calculated"}}]}`}}
	s, runs, owner, executor, exchange := admitPolicy(t, []tool.Definition{ui.ShowDefinition()}, calls)
	if _, err := s.db.Exec(`CREATE TRIGGER ui_result_fault BEFORE UPDATE ON tool_invocation WHEN NEW.tool_name='builtin__intelligent_ui_show' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'owned result commit fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(ctx, exchange, calls); err == nil {
		t.Fatal("result commit fault was not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NULL`) != 1 {
		t.Fatal("UI staging did not survive its separate result commit")
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WebWidget(ctx, ref); err != agent.ErrNotFound {
		t.Fatal("unpublished UI was visible", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER ui_result_fault`); err != nil {
		t.Fatal(err)
	}
	if err := runs.Release(ctx, owner); err != nil {
		t.Fatal(err)
	}
	recovered, err := runs.Claim(ctx, owner.RunID())
	if err != nil {
		t.Fatal(err)
	}
	executor.Store = runs.ToolStore(recovered)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	staged, stagedErr := runs.StagedUIPreviews(ctx, recovered)
	if stagedErr != nil || len(staged) != 1 || staged[0].Key != exchange+":show" || staged[0].Order == "" {
		t.Fatal("recovery did not restore staged preview", staged, stagedErr)
	}
	if _, err := runs.StagedUIPreviews(ctx, owner); err != agent.ErrClaimLost {
		t.Fatal("stale claim read preview", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 1 {
		t.Fatal("recovery duplicated the presentation")
	}
}

type uiStreamingProvider struct {
	*loopProvider
	raw  string
	fail bool
}

func (p uiStreamingProvider) Generate(ctx context.Context, req llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	if req.Messages[len(req.Messages)-1].Role != "tool" {
		sink(llm.GenerationEvent{Kind: "ui_delta", ToolCall: &llm.ToolCall{ID: "stream-call", Name: ui.ShowName, Arguments: p.raw[:len(p.raw)-4]}})
		if p.fail {
			return llm.GenerationResult{}, llm.ProviderUnavailable
		}
	}
	return p.loopProvider.Generate(ctx, req, sink)
}
func (p uiStreamingProvider) Continue(ctx context.Context, req llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.Generate(ctx, req, sink)
}
func TestMainDirectUIPreviewIsEphemeralAndWithdrawnOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, _, a, p := childFixture(t, ui.ShowDefinition())
			raw := `{"title":"Stream","fallback":"Answer","components":[{"name":"Table","props":{"title":"Stream","columns":["Name"],"rows":[["A"]],"source":"User"}}]}`
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "Done"}, nil
				}
				return calls(ui.ShowName, raw), nil
			}
			a.Provider = uiStreamingProvider{loopProvider: p, raw: raw, fail: fail}
			var early, discarded, committed atomic.Bool
			a.Sink = func(e agent.Event) {
				if e.Kind == "ui_preview_update" && e.UIPreview != nil && len(e.UIPreview.Components) > 0 && scalar(t, s, `SELECT count(*) FROM ui_widget`) == 0 {
					early.Store(true)
				}
				if e.Kind == "ui_preview_discarded" {
					discarded.Store(true)
				}
				if e.Kind == "ui_preview_committed" {
					committed.Store(true)
				}
			}
			mustSubmit(t, a, "preview", "Show")
			err := a.Drain(context.Background())
			if !early.Load() {
				t.Fatal("direct UI did not preview before execution")
			}
			if fail {
				if err == nil || !discarded.Load() || committed.Load() || scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 {
					t.Fatal("failed prefix became a delivered UI", err)
				}
			} else if err != nil || !committed.Load() || scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 1 {
				t.Fatal("final delivery changed", err)
			}
			if scalar(t, s, `SELECT count(*) FROM run_trace WHERE json_extract(event_json,'$.kind') LIKE 'ui_preview_%'`) != 0 {
				t.Fatal("preview persisted")
			}
		})
	}
}

func TestUIPreviewUsesAcceptedArgumentsRatherThanCallerPayload(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "show", Name: ui.ShowName, Arguments: `{"title":"Accepted","fallback":"Answer","components":[{"name":"Table","props":{"title":"A","columns":["Name"],"rows":[["A"]],"source":"User"}}]}`}}
	_, _, _, executor, exchange := admitPolicy(t, []tool.Definition{ui.ShowDefinition()}, calls)
	invs, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := executor.Store.Prepare(ctx, invs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	inv.Call.Arguments = `{"title":"Forged caller payload"}`
	observed := ""
	ctx = ui.WithPreviewObserver(ctx, func(_ string, raw string, _ string) { observed = raw })
	result, err := executor.Store.(tool.HostStore).ExecuteBuiltin(ctx, inv)
	if err != nil || result.Error != nil || ui.Hash(json.RawMessage(observed)) != ui.Hash(json.RawMessage(calls[0].Arguments)) {
		t.Fatal("preview bypassed the accepted invocation", result, err, observed)
	}
}

func TestMainUIPreviewKeepsDistinctInvocationsForRepeatedProviderCallID(t *testing.T) {
	s, _, a, p := childFixture(t, ui.ShowDefinition())
	previews := map[string]bool{}
	var mu sync.Mutex
	a.Sink = func(e agent.Event) {
		if e.Kind == "ui_preview_update" && e.UIPreview != nil {
			mu.Lock()
			previews[e.PreviewKey] = true
			mu.Unlock()
		}
	}
	turn := 0
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		turn++
		if turn > 2 {
			return llm.GenerationResult{Text: "Both comparisons are ready."}, nil
		}
		widget := ui.Widget{Title: fmt.Sprintf("Comparison %d", turn), Fallback: fmt.Sprintf("Result %d", turn), Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{"title":"Result","columns":["Value"],"rows":[["42"]],"source":"Fixture"}`)}}}
		raw, _ := json.Marshal(widget)
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "repeated-provider-id", Name: ui.ShowName, Arguments: string(raw)}}}, nil
	}
	mustSubmit(t, a, "repeated-ui-call", "Show two comparisons")
	mustDrain(t, a)
	mu.Lock()
	defer mu.Unlock()
	if len(previews) != 2 || scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 2 {
		t.Fatal("provider call ID overwrote another preview", previews)
	}
}

func TestMainDirectUIAcceptsCatalogSizedVisualWithoutCodeMode(t *testing.T) {
	s, _, a, p := childFixture(t, ui.ShowDefinition())
	input := modelInput()
	input.ModelID = "large-visual"
	input.ContextWindow = 32768
	input.MaxOutputTokens = 4096
	activateLoop(t, s, input)
	props, _ := json.Marshal(ui.SandboxedUI{InitialHeight: 300, PlaceholderMessages: []string{}, HTML: "<p>" + strings.Repeat("Detailed explanation. ", 1000) + "</p>", CSS: "", JSFunctions: "", JSExpressions: []string{}})
	raw, _ := json.Marshal(ui.Widget{Title: "Mechanism", Fallback: "Complete explanation", Components: []ui.Component{{Name: "generateSandboxedUi", Props: props}}})
	if len(raw) <= 16<<10 || len(raw) >= 64<<10 {
		t.Fatal("fixture must exceed the ordinary argument limit within the UI limit")
	}
	p.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "The mechanism is shown."}, nil
		}
		return calls(ui.ShowName, string(raw)), nil
	}
	mustSubmit(t, a, "large-direct-ui", "Explain the mechanism")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__codemode_execute'`) != 0 {
		t.Fatal("a catalog-sized visual required Code Mode or failed delivery")
	}
}

func TestCompactModelDeclarationCannotRelaxUIExecution(t *testing.T) {
	s, _, a, p := childFixture(t, ui.ShowDefinition())
	raw := `{"title":"Values","fallback":"Preserved answer","components":[{"name":"Table","props":{"title":"Values","columns":"not an array","rows":[],"source":"User"}}]}`
	if !tool.Validate(ui.ShowDefinition().Spec.ModelInputSchema, raw) {
		t.Fatal("fixture must fit the compact declaration")
	}
	p.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		last := request.Messages[len(request.Messages)-1]
		if last.Role == "tool" {
			if !strings.Contains(last.Text, "INVALID_ARGUMENT") {
				t.Fatal("compact metadata bypassed actual component validation", last)
			}
			return llm.GenerationResult{Text: "The table was rejected."}, nil
		}
		return calls(ui.ShowName, raw), nil
	}
	mustSubmit(t, a, "invalid-compact-ui", "Show the values")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 {
		t.Fatal("invalid component reached a durable presentation")
	}
}

func TestPublicUISectionsPreserveOrderWithoutPublishingToolNarration(t *testing.T) {
	s, _, a, p := childFixture(t, ui.ShowDefinition())
	var step atomic.Int32
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		n := step.Add(1)
		if n > 2 {
			return llm.GenerationResult{Text: "Finally, follow the return path."}, nil
		}
		raw, _ := json.Marshal(ui.Widget{Title: fmt.Sprintf("Section %d", n), Text: fmt.Sprintf("Public explanation %d", n), Fallback: fmt.Sprintf("Visual alternative %d", n), Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{"title":"Roles","columns":["Role"],"rows":[["Main"]],"source":"User"}`)}}})
		result := calls(ui.ShowName, string(raw))
		result.Text = "PRIVATE TOOL WORK"
		return result, nil
	}
	mustSubmit(t, a, "public-sections", "Explain the architecture")
	mustDrain(t, a)
	var raw string
	if err := s.db.QueryRow(`SELECT message_json FROM conversation_intent WHERE message_id LIKE 'reply:%' ORDER BY rowid DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m conversation.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Content) != 3 || m.Content[0].Text != "Public explanation 1\n\nVisual alternative 1" || m.Content[1].Text != "Public explanation 2\n\nVisual alternative 2" || m.Content[2].Text != "Finally, follow the return path." || strings.Contains(raw, "PRIVATE TOOL WORK") {
		t.Fatal("public answer order or authority lost", m)
	}
	projection, err := s.WebProjection(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(projection, &spec); err != nil {
		t.Fatal(err)
	}
	bubbles, err := s.WebBubbles(context.Background(), m, spec)
	if err != nil || len(bubbles) != 5 {
		t.Fatal(bubbles, err)
	}
	for i, want := range []string{"Public explanation 1", "Roles", "Public explanation 2", "Roles", "Finally, follow the return path."} {
		encoded, _ := json.Marshal(bubbles[i])
		if !strings.Contains(string(encoded), want) {
			t.Fatal("interleaved order lost", i, string(encoded))
		}
	}
	if bubbles[1].Components[0].WidgetRef == "" || !strings.HasSuffix(bubbles[1].Components[0].WidgetRef, "#0") {
		t.Fatal("paragraph changed component identity", bubbles[1])
	}
	// A frozen mismatch must not invent a new layout from mutable projection data.
	spec.Components[0].Text = "forged"
	if got, err := s.WebBubbles(context.Background(), m, spec); err != nil || len(got) != 0 {
		t.Fatal("unverified prose was rearranged", got, err)
	}
}

func TestUIStagedRecoveryKeepsAcceptedOrderDespiteReverseCompletion(t *testing.T) {
	ctx := context.Background()
	var calls []llm.ToolCall
	for _, title := range []string{"First", "Second"} {
		raw, _ := json.Marshal(ui.Widget{Title: title, Text: "Explain " + title, Fallback: title, Components: []ui.Component{{Name: "Table", Props: json.RawMessage(`{"title":"Roles","columns":["Role"],"rows":[["Main"]],"source":"User"}`)}}})
		calls = append(calls, llm.ToolCall{ID: title, Name: ui.ShowName, Arguments: string(raw)})
	}
	_, runs, owner, executor, exchange := admitPolicy(t, []tool.Definition{ui.ShowDefinition()}, calls)
	invs, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	for i := len(invs) - 1; i >= 0; i-- {
		inv, err := executor.Store.Prepare(ctx, invs[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := executor.Store.Start(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		result, err := executor.Store.(tool.HostStore).ExecuteBuiltin(ctx, inv)
		if err != nil || result.Error != nil {
			t.Fatal(result, err)
		}
		if err := executor.Store.Finish(ctx, inv.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	if err := runs.Release(ctx, owner); err != nil {
		t.Fatal(err)
	}
	recovered, err := runs.Claim(ctx, owner.RunID())
	if err != nil {
		t.Fatal(err)
	}
	previews, err := runs.StagedUIPreviews(ctx, recovered)
	if err != nil || len(previews) != 2 {
		t.Fatal(previews, err)
	}
	if previews[0].Key != exchange+":First" || previews[1].Key != exchange+":Second" || previews[0].Order == "" || previews[0].Order >= previews[1].Order {
		t.Fatal("recovery lost accepted order or its private sort keys", previews)
	}
}
