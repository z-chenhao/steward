package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
)

func tableViewWork(t *testing.T) (*Store, *Runs, agent.Actor) {
	t.Helper()
	raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-table.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Props json.RawMessage `json:"props"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	store, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, store, runs)
	input := `{"title":"Table","fallback":"Original Table","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"Table","props":` + string(original.Props) + `}]}`
	step := 0
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		step++
		if step == 1 {
			return calls(ui.ShowName, input), nil
		}
		return llm.GenerationResult{Text: "Table ready."}, nil
	}
	mustSubmit(t, actor, "original-table", "Present original Table")
	return store, runs, actor
}

func TestUITableManagedSnapshotSurvivesReceiptFailureAndForgetRevokesExport(t *testing.T) {
	ctx := context.Background()
	store, runs, actor := tableViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_table_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'table receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt failure not exercised", err)
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, store, `SELECT count(*) FROM ui_table_view_snapshot`) != 1 || scalar(t, store, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-table%'`) != 0 {
		t.Fatal("pure Table staging lost or reserved as effect")
	}
	var ref, saved string
	if err := store.db.QueryRow(`SELECT w.ui_ref,p.result_json FROM ui_widget w JOIN ui_table_view_snapshot p USING(invocation_id)`).Scan(&ref, &saved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.WebTableViewSource(ctx, ref+"#1"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("unpublished Table exposed", err)
	}
	if _, _, err := store.WebTableExportSource(ctx, ref+"#1", []int{0}); err == nil {
		t.Fatal("unpublished export granted")
	}
	runs.plugins = nil // Real recovery cannot call the candidate again.
	if _, err := store.db.Exec(`DROP TRIGGER fail_table_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	view, graph, err := store.WebTableViewSource(ctx, ref+"#1")
	if err != nil || graph == "" || !strings.Contains(view.HTML, "Original table evidence") || view.Worker == "" || len(view.Contexts) != 8 {
		t.Fatal("original accepted Table not recovered", view, graph, err)
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
		if component.Name == "Table" {
			found = true
			if component.ViewRef != ref+"#1" {
				t.Fatal("original Table ordinal not bound", component)
			}
		}
	}
	if !found {
		t.Fatal("Table missing from real projection")
	}
	if bubbles, err := store.WebBubbles(ctx, message, spec); err != nil || len(bubbles) == 0 {
		t.Fatal("original reference mismatched bubbles", bubbles, err)
	}
	export, exportGraph, err := store.WebTableExportSource(ctx, ref+"#1", []int{4, 3})
	if err != nil || exportGraph != graph || export.Title != "Original table evidence" || len(export.Rows) != 2 || export.Rows[0][2] != "@unsafe" || export.Rows[1][2] != `中文, "quoted"` {
		t.Fatal("export substituted original values/order", export, exportGraph, err)
	}
	for _, rows := range [][]int{nil, {-1}, {200}, {0, 0}} {
		if _, _, err := store.WebTableExportSource(ctx, ref+"#1", rows); err == nil {
			t.Fatal("foreign rows granted", rows)
		}
	}
	if empty, _, err := store.WebTableExportSource(ctx, ref+"#1", []int{}); err != nil || empty.Rows == nil || len(empty.Rows) != 0 {
		t.Fatal("zero rows changed export shape", empty, err)
	}
	var after string
	if err = store.db.QueryRow(`SELECT result_json FROM ui_table_view_snapshot`).Scan(&after); err != nil || after != saved {
		t.Fatal("recovery changed frozen Table", err)
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
	if _, _, err = store.WebTableViewSource(ctx, ref+"#1"); err == nil {
		t.Fatal("Forget retained Table read grant")
	}
	if _, _, err = store.WebTableExportSource(ctx, ref+"#1", []int{0}); err == nil {
		t.Fatal("Forget retained original export grant")
	}
}

func TestUITableManagedSnapshotRollbackDoesNotReserveEffects(t *testing.T) {
	store, _, actor := tableViewWork(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_table_snapshot BEFORE INSERT ON ui_table_view_snapshot BEGIN SELECT RAISE(ABORT,'table snapshot fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("snapshot failure not exercised", err)
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM ui_table_view_snapshot`) != 0 || scalar(t, store, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-table%'`) != 0 {
		t.Fatal("partial artifact or unknown effect reserved")
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_table_snapshot`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	var ref string
	if err := store.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if view, _, err := store.WebTableViewSource(context.Background(), ref+"#1"); err != nil || view.Worker == "" {
		t.Fatal("original input did not recover", err)
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, store, `SELECT count(*) FROM ui_table_view_snapshot`) != 1 {
		t.Fatal("retry duplicated Table")
	}
}

func TestUITableManagedPreviewUsesOriginalPureServiceWithoutDeliveryGrants(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, store, runs)
	receipt := mustSubmit(t, actor, "table-preview", "Show Table")
	claim, err := runs.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer runs.Release(ctx, claim)
	raw := `{"title":"Draft","components":[{"name":"plan_visualization","props":{"approach":"Original","technology":"Go","key_elements":[]}},{"name":"Table","props":{"title":"Original","columns":["N"],"rows":[["42"]],"source":"Evidence"}`
	preview, valid, err := runs.UIPreview(ctx, claim, raw)
	view := preview.TableViews[1]
	if err != nil || !valid || !preview.TableManaged || len(preview.TableViews) != 1 || !strings.Contains(view.HTML, "Evidence") || view.Worker == "" || view.CSS == "" || len(view.Contexts) != 0 {
		t.Fatal("owning preview or privacy boundary missing", preview, valid, err)
	}
	wire, _ := json.Marshal(preview)
	if !strings.Contains(string(wire), `"table_view":true`) || strings.Contains(string(wire), "answer-table") || strings.Contains(string(wire), "onmessage") {
		t.Fatal("private view broadcast", string(wire))
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM ui_table_view_snapshot`) != 0 || scalar(t, store, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview created delivery/effect")
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
		t.Fatal("Forget did not revoke preview", err)
	}
}
