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

func chartViewWork(t *testing.T) (*Store, *Runs, agent.Actor) {
	t.Helper()
	store, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtTableBoundary(t, store, runs, false, true)
	step := 0
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		step++
		if step == 1 {
			return calls(ui.ShowName, `{"title":"Charts","fallback":"Original Trend","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"steward.lineChart","props":{"title":"Trend","description":"Original","data":[{"label":"A","value":10},{"label":"B","value":42}]}}]}`), nil
		}
		return llm.GenerationResult{Text: "Chart ready."}, nil
	}
	mustSubmit(t, actor, "original-chart", "Present original chart")
	return store, runs, actor
}
func TestUIChartSnapshotReceiptRecoveryRetainsOriginalGraphAndForgetFence(t *testing.T) {
	ctx := context.Background()
	store, runs, actor := chartViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_chart_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'chart receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt fault not reached", err)
	}
	var ref, saved, graph string
	if err := store.db.QueryRow(`SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_chart_view_snapshot p USING(invocation_id)`).Scan(&ref, &saved, &graph); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.WebChartViewSource(ctx, ref+"#1"); err == nil {
		t.Fatal("unpublished chart readable")
	}
	runs.plugins = nil
	if _, err := store.db.Exec(`DROP TRIGGER fail_chart_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	view, original, readGraph, err := store.WebChartViewSource(ctx, ref+"#1")
	if err != nil || readGraph != graph || view.Component != 1 || view.Worker == "" || original.Name != "steward.lineChart" {
		t.Fatal("first recovered read lost original graph", readGraph, err)
	}
	exported, exportGraph, err := store.WebChartExportSource(ctx, ref+"#1", []int{1, 0})
	if err != nil || exportGraph != graph || len(exported.Rows) != 2 || string(exported.Rows[0][0]) != `"B"` || string(exported.Rows[0][1]) != "42" {
		t.Fatal("original chart export not retained", err)
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
		if component.Name == "steward.lineChart" {
			found = true
			if component.ViewRef != ref+"#1" {
				t.Fatal("chart original reference not bound", component.ViewRef)
			}
		}
	}
	if !found {
		t.Fatal("chart missing from real projection")
	}
	var after string
	if err = store.db.QueryRow(`SELECT result_json FROM ui_chart_view_snapshot`).Scan(&after); err != nil || after != saved {
		t.Fatal("recovery recomputed chart", err)
	}
	if scalar(t, store, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-chart%'`) != 0 {
		t.Fatal("pure policy reserved as effect")
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
	if _, _, _, err = store.WebChartViewSource(ctx, ref+"#1"); err == nil {
		t.Fatal("Forget retained chart grant")
	}
	if _, _, err = store.WebChartExportSource(ctx, ref+"#1", []int{0}); err == nil {
		t.Fatal("Forget retained original CSV grant")
	}
}
func TestUIChartSnapshotFailureRollsBackWidgetWithoutEffect(t *testing.T) {
	store, _, actor := chartViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_chart_snapshot BEFORE INSERT ON ui_chart_view_snapshot BEGIN SELECT RAISE(ABORT,'chart snapshot fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("snapshot fault not reached", err)
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM ui_chart_view_snapshot`) != 0 {
		t.Fatal("partial chart staged")
	}
}

func TestUIChartPrivatePreviewUsesOriginalGraphWithoutDeliveryGrants(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtTableBoundary(t, store, runs, false, true)
	receipt := mustSubmit(t, actor, "chart-preview", "Show chart")
	claim, err := runs.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer runs.Release(ctx, claim)
	raw := `{"title":"Draft","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"steward.lineChart","props":{"title":"Trend","description":"Original","data":[{"label":"A","value":10}]}`
	preview, valid, err := runs.UIPreview(ctx, claim, raw)
	view := preview.ChartViews[1]
	if err != nil || !valid || !preview.ChartManaged || len(preview.ChartViews) != 1 || view.Name != "steward.lineChart" || view.Title != "Trend" || view.Worker == "" {
		t.Fatal("original chart preview unavailable", valid, err)
	}
	wire, _ := json.Marshal(preview)
	if !strings.Contains(string(wire), `"chart_view":true`) || strings.Contains(string(wire), "onmessage") || strings.Contains(string(wire), "ChartViews") {
		t.Fatal("private Worker broadcast", string(wire))
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM ui_chart_view_snapshot`) != 0 || scalar(t, store, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview acquired delivery/effect")
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
	if _, valid, err = runs.UIPreview(ctx, claim, raw); err == nil || valid {
		t.Fatal("Forget retained preview", err)
	}
}
