package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func planViewWork(t *testing.T) (*Store, *Runs, agent.Actor) {
	t.Helper()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, s, r)

	var step atomic.Int32
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		if step.Add(1) == 1 {
			return calls(ui.ShowName, `{"title":"Plan","fallback":"Plan","components":[{"name":"plan_visualization","props":{"approach":"Ship core","technology":"Go","key_elements":["Loop","Host"]}}]}`), nil
		}
		return llm.GenerationResult{Text: "Plan ready."}, nil
	}
	mustSubmit(t, a, "plan-receipt-recovery", "Present the original plan")
	return s, r, a

}

func TestUIPlanViewSnapshotSurvivesReceiptFailureWithoutCandidateReplay(t *testing.T) {
	s, r, a := planViewWork(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_plan_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'plan receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt fault not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_plan_view_snapshot`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-plan%'`) != 0 {
		t.Fatal("pure view was reserved as an effect or lost its artifact")
	}
	var ref, saved string
	if err := s.db.QueryRow(`SELECT w.ui_ref,p.result_json FROM ui_widget w JOIN ui_plan_view_snapshot p USING(invocation_id)`).Scan(&ref, &saved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WebPlanViewSource(ctx, ref+"#0"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("unpublished plan exposed", err)
	}
	r.plugins = nil // Loss of candidate execution must not alter accepted staging.
	if _, err := s.db.Exec(`DROP TRIGGER fail_plan_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	view, graph, err := s.WebPlanViewSource(ctx, ref+"#0")
	if err != nil || graph == "" || !strings.Contains(view.HTML, "Plan: Go") || !strings.Contains(view.HTML, "Ship core") || view.CSS == "" {
		t.Fatal("original view did not recover", view, graph, err)
	}
	var messageRaw string
	if err = s.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=?`, ref).Scan(&messageRaw); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err = json.Unmarshal([]byte(messageRaw), &message); err != nil {
		t.Fatal(err)
	}
	projected, err := s.WebProjection(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(projected, &spec); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range spec.Components {
		if c.Name == "plan_visualization" {
			found = true
			if c.ViewRef != ref+"#0" {
				t.Fatal("view not bound to original position", c)
			}
		}
	}
	if !found {
		t.Fatal("managed plan missing from actual projection")
	}
	bubbles, err := s.WebBubbles(ctx, message, spec)
	if err != nil || len(bubbles) == 0 {
		t.Fatal("view metadata changed frozen projection identity", bubbles, err)
	}
	var after string
	if err = s.db.QueryRow(`SELECT result_json FROM ui_plan_view_snapshot`).Scan(&after); err != nil || saved != after {
		t.Fatal("recovery changed frozen view", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_plan_view_snapshot`) != 1 {
		t.Fatal("recovery duplicated view")
	}
	if _, _, err = s.WebPlanViewSource(ctx, ref+"#1"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("unbound index exposed", err)
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
	if _, _, err = s.WebPlanViewSource(ctx, ref+"#0"); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("retained view bypassed actual Forget", err)
	}

}

func TestUIPlanViewSnapshotRejectsMissingDuplicateAndOtherComponentSlots(t *testing.T) {
	document := `{"title":"Plan","fallback":"Plan","components":[{"name":"plan_visualization","props":{"approach":"A","technology":"Go","key_elements":[]}},{"name":"Table","props":{"title":"T","columns":["A"],"rows":[],"source":"S"}},{"name":"plan_visualization","props":{"approach":"B","technology":"Go","key_elements":[]}}]}`
	for _, width := range []int{0, -1, 4097} {
		result := contract.PlanViewsResult{Views: []contract.PlanView{{Component: 0, HTML: "x", CSS: "p{}", CompactMaxWidth: width}, {Component: 2, HTML: "x", CSS: "p{}", CompactMaxWidth: 760}}}
		if validPlanViews(document, result) == nil {
			t.Fatal("unbounded viewport policy admitted", width)
		}
	}
	// A strict decode also rejects any candidate-supplied Host identity.
	for _, raw := range []string{`{"views":[],"error_code":""}`, `{"views":[],"error_code":"","message_id":"forged"}`, `{"views":[{"component":1,"html":"<p>x</p>","css":"p{}","compact_max_width":760},{"component":2,"html":"x","css":"p{}","compact_max_width":760}],"error_code":""}`, `{"views":[{"component":0,"html":"x","css":"p{}","compact_max_width":760},{"component":0,"html":"x","css":"p{}","compact_max_width":760}],"error_code":""}`} {
		var result contract.PlanViewsResult
		err := rsiplugin.DecodeResult([]byte(raw), &result)
		if err == nil {
			err = validPlanViews(document, result)
		}
		if err == nil {
			t.Fatal("invalid slot recipe admitted", raw)
		}
	}
}

func TestUIPlanViewSnapshotRollbackDoesNotReserveEffect(t *testing.T) {
	s, _, a := planViewWork(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_plan_snapshot BEFORE INSERT ON ui_plan_view_snapshot BEGIN SELECT RAISE(ABORT,'plan snapshot fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("snapshot failure not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_plan_view_snapshot`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-plan%'`) != 0 {
		t.Fatal("failed pure computation reserved an artifact or effect")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_plan_snapshot`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if view, _, err := s.WebPlanViewSource(ctx, ref+"#0"); err != nil || !strings.Contains(view.HTML, "Ship core") {
		t.Fatal("original input did not recover after rollback", view, err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_plan_view_snapshot`) != 1 {
		t.Fatal("recovery duplicated accepted view")
	}
}

func TestUIPlanViewRefBindingRejectsMissingManagedReceipt(t *testing.T) {
	document := `{"title":"Plan","fallback":"Plan","components":[{"name":"plan_visualization","props":{"approach":"A","technology":"Go","key_elements":[]}}]}`
	components := []ui.Component{{Name: "plan_visualization"}}
	if err := bindPlanViewRefs(document, "ui:original", components, sql.NullString{Valid: true}); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("managed view silently fell back", err)
	}
	if err := bindPlanViewRefs(document, "ui:original", components, sql.NullString{}); err != nil || components[0].ViewRef != "" {
		t.Fatal("historical view was reinterpreted", components, err)
	}
}

func TestUIPlanViewPreviewUsesOriginalPureServiceAndPrivacyFence(t *testing.T) {
	ctx := context.Background()
	s, r, a, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, s, r)
	receipt := mustSubmit(t, a, "plan-preview", "Show plan")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	raw := `{"title":"Draft","components":[{"name":"plan_visualization","props":{"technology":"Go","approach":"Ship`
	preview, valid, err := r.UIPreview(ctx, c, raw)
	if err != nil || !valid || !preview.PlanManaged || len(preview.PlanViews) != 1 || !strings.Contains(preview.PlanViews[0].HTML, "Ship") || preview.PlanViews[0].CompactMaxWidth != 760 {
		t.Fatal("actual frozen preview producer not used", preview, valid, err)
	}
	wire, _ := json.Marshal(preview)
	if strings.Contains(string(wire), "<details") || strings.Contains(string(wire), "compact_max_width") {
		t.Fatal("private recipe leaked into event wire", string(wire))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_plan_view_snapshot`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview created durable facts or effect calls")
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRow(`SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, valid, err = r.UIPreview(ctx, c, raw); err == nil || valid {
		t.Fatal("Forget did not revoke preview", err)
	}
}
