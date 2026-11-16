package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func validPlanViews(document string, result contract.PlanViewsResult) error {
	w, err := managedUIDocument(document)
	if err != nil || result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	expected := map[int]bool{}
	for i, c := range w.Components {
		if c.Name == "plan_visualization" {
			expected[i] = true
		}
	}
	return validPlanViewSlots(expected, result)
}

func validPlanViewSlots(expected map[int]bool, result contract.PlanViewsResult) error {
	if result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	if len(result.Views) != len(expected) {
		return rsi.ErrInvalid
	}
	for _, v := range result.Views {
		if v.CompactMaxWidth < 1 || v.CompactMaxWidth > 4096 || !expected[v.Component] || v.HTML == "" || v.CSS == "" || len(v.HTML) > 512<<10 || len(v.CSS) > 16<<10 || !utf8.ValidString(v.HTML) || !utf8.ValidString(v.CSS) || strings.ContainsRune(v.HTML, 0) || strings.ContainsRune(v.CSS, 0) {
			return rsi.ErrInvalid
		}
		delete(expected, v.Component)
	}
	return nil
}

// WebPlanViewSource reads a delivered, account-owned component's accepted pure
// output. No candidate, active selection, credential or arbitrary path is read.
func (s *Store) WebPlanViewSource(ctx context.Context, ref string) (contract.PlanView, string, error) {
	base, index, ok := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return contract.PlanView{}, "", agent.ErrNotFound
	}
	var document, receipt, raw, graph string
	var managed bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_plan_view_snapshot p USING(invocation_id) JOIN run r USING(run_id) JOIN rsi_run b USING(run_id) JOIN tool_invocation t USING(invocation_id) JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded' AND p.graph_revision=b.graph_revision AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value=?)`, base, contract.PlanViews).Scan(&document, &managed, &receipt, &raw, &graph)
	if err != nil {
		return contract.PlanView{}, "", agent.ErrNotFound
	}
	// Retain the existing accepted-normalizer/privacy boundary. Forget clears
	// that receipt; a retained view must not make revoked UI visible again.
	if _, _, err = storedUI(document, managed, receipt); err != nil {
		return contract.PlanView{}, "", agent.ErrStorage
	}
	var result contract.PlanViewsResult
	if err = rsiplugin.DecodeResult(json.RawMessage(raw), &result); err != nil {
		return contract.PlanView{}, "", agent.ErrStorage
	}
	if err = validPlanViews(document, result); err != nil {
		return contract.PlanView{}, "", agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component == i {
			return view, graph, nil
		}
	}
	return contract.PlanView{}, "", agent.ErrNotFound
}

// NULL is an explicit historical boundary. Empty managed receipts are corrupt,
// not a reason to render the current native implementation.
const uiPlanColumn = `CASE WHEN EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-plan-view/1') THEN coalesce((SELECT p.result_json FROM ui_plan_view_snapshot p JOIN rsi_run b ON b.run_id=w.run_id WHERE p.invocation_id=w.invocation_id AND p.graph_revision=b.graph_revision),'') ELSE NULL END`

func bindPlanViewRefs(document, ref string, components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.PlanViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil || validPlanViews(document, result) != nil {
		return agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component >= len(components) {
			return agent.ErrStorage
		}
		components[view.Component].ViewRef = ref + "#" + strconv.Itoa(view.Component)
	}
	return nil
}
