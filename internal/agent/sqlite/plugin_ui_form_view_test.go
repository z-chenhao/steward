package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/conversation"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
)

func formViewWork(t *testing.T) (*Store, *Runs, agent.Actor) {
	t.Helper()
	store, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtFormBoundary(t, store, runs, false, true)
	step := 0
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		step++
		if step == 1 {
			return calls(ui.ShowName, `{"title":"Forms","fallback":"Original form","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"steward.form","props":{"title":"Original form","fields":[{"name":"notes","label":"Notes","type":"textarea"},{"name":"confirm","label":"Confirm","type":"boolean"}]}}]}`), nil
		}
		return llm.GenerationResult{Text: "Form ready."}, nil
	}
	mustSubmit(t, actor, "original-form", "Present original form view")
	return store, runs, actor
}
func TestUIFormViewSnapshotReceiptRecoveryRetainsOriginalGraphAndForgetFence(t *testing.T) {
	ctx := context.Background()
	store, runs, actor := formViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_form_view_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'form view receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt fault not reached", err)
	}
	var ref, saved, graph string
	if err := store.db.QueryRow(`SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_form_view_snapshot p USING(invocation_id)`).Scan(&ref, &saved, &graph); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.WebFormViewSource(ctx, ref+"#1"); err == nil {
		t.Fatal("unpublished form view readable")
	}
	runs.plugins = nil
	if _, err := store.db.Exec(`DROP TRIGGER fail_form_view_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	view, original, readGraph, err := store.WebFormViewSource(ctx, ref+"#1")
	if err != nil || readGraph != graph || view.Component != 1 || len(view.Fields) != 2 || view.Fields[1].Default != "false" || original.Name != "steward.form" {
		t.Fatal("first recovered read lost original graph", readGraph, err)
	}
	var messageJSON string
	if err = store.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=?`, ref).Scan(&messageJSON); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err = json.Unmarshal([]byte(messageJSON), &message); err != nil {
		t.Fatal(err)
	}
	projected, err := store.WebProjection(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(projected, &spec); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range spec.Components {
		if component.Name == "steward.form" {
			found = true
			if component.ViewRef != ref+"#1" {
				t.Fatal("form view original reference not bound", component.ViewRef)
			}
		}
	}
	if !found {
		t.Fatal("form view missing from real projection")
	}
	var after string
	if err = store.db.QueryRow(`SELECT result_json FROM ui_form_view_snapshot`).Scan(&after); err != nil || after != saved {
		t.Fatal("recovery recomputed form view", err)
	}
	if scalar(t, store, `SELECT count(*) FROM rsi_call WHERE operation_id NOT LIKE 'ui-definition:%' AND operation_id NOT LIKE 'ui-normalize:%'`) != 0 {
		t.Fatal("pure view preparation left an extra durable operation")
	}
	if err = store.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		return runs.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = store.WebFormViewSource(ctx, ref+"#1"); err == nil {
		t.Fatal("Forget retained form view grant")
	}

}
func TestUIFormViewSnapshotFailureRollsBackWidgetWithoutEffect(t *testing.T) {
	store, _, actor := formViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_form_view_snapshot BEFORE INSERT ON ui_form_view_snapshot BEGIN SELECT RAISE(ABORT,'form view snapshot fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("snapshot fault not reached", err)
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM ui_form_view_snapshot`) != 0 {
		t.Fatal("partial form view staged")
	}
}

func TestUIFormViewPreviewUsesOriginalPureServiceWithoutPublication(t *testing.T) {
	ctx := context.Background()
	s, r, a, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtFormBoundary(t, s, r, false, true)
	receipt := mustSubmit(t, a, "form-preview", "Show form")
	claim, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, claim)
	raw := `{"title":"Draft","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"steward.form","props":{"title":"Original form","fields":[{"name":"confirm","label":"Confirm","type":"boolean"}]}`
	preview, valid, err := r.UIPreview(ctx, claim, raw)
	view := preview.FormViews[1]
	if err != nil || !valid || !preview.FormManaged || len(preview.FormViews) != 1 || view.Props.Title != "Original form" || view.Defaults["confirm"] != "false" {
		t.Fatal("original pure form preview unavailable", valid, err, view)
	}
	wire, _ := json.Marshal(preview)
	for _, forbidden := range []string{`"form_id"`, `"message_id"`, `"widget_ref"`, `"view_ref"`, `"worker"`, `"script"`} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatal("preview acquired authority", forbidden)
		}
	}
	if !strings.Contains(string(wire), `"form_view":true`) || !strings.Contains(string(wire), `"form_views":{"1":`) {
		t.Fatal("static display missing from existing wire", string(wire))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_form_view_snapshot`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview acquired publication or durable operation")
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
			return err
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.UIPreview(ctx, claim, raw); err == nil {
		t.Fatal("Forget retained preview claim")
	}
}
