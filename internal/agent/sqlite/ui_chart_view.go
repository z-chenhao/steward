package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strconv"
	"strings"
	"unicode/utf8"
)

func isChartComponent(name string) bool {
	switch name {
	case "barChart", "pieChart", "steward.lineChart", "steward.scatterChart":
		return true
	}
	return false
}

// Host accepts only a complete contribution for the original chart slots.
// No chart policy, ref, model context or execution authority is supplied here.
func validChartViews(document string, result contract.ChartViewsResult) error {
	w, err := managedUIDocument(document)
	if err != nil || result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	expected := map[int]bool{}
	for i, c := range w.Components {
		if isChartComponent(c.Name) {
			expected[i] = true
		}
	}
	if len(expected) != len(result.Views) {
		return rsi.ErrInvalid
	}
	for _, view := range result.Views {
		if !expected[view.Component] || view.Worker == "" || len(view.Worker) > 16<<10 || !utf8.ValidString(view.Worker) || strings.ContainsRune(view.Worker, 0) {
			return rsi.ErrInvalid
		}
		for _, text := range []struct {
			value string
			limit int
		}{{view.Title, 300}, {view.Description, 2000}, {view.Unit, 64}} {
			if !utf8.ValidString(text.value) || utf8.RuneCountInString(text.value) > text.limit {
				return rsi.ErrInvalid
			}
		}
		for _, column := range view.Columns {
			if !utf8.ValidString(column.Header) || utf8.RuneCountInString(column.Header) > 300 {
				return rsi.ErrInvalid
			}
		}
		if _, err := chartExportFacts(w.Components[view.Component], view, []int{}); err != nil {
			return rsi.ErrInvalid
		}
		delete(expected, view.Component)
	}
	return nil
}

func (s *Store) WebChartViewSource(ctx context.Context, ref string) (contract.ChartView, ui.Component, string, error) {
	base, index, ok := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return contract.ChartView{}, ui.Component{}, "", agent.ErrNotFound
	}
	var document, receipt, raw, graph string
	var managed bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_chart_view_snapshot p USING(invocation_id) JOIN run r USING(run_id) JOIN rsi_run b USING(run_id) JOIN tool_invocation t USING(invocation_id) JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded' AND p.graph_revision=b.graph_revision AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value=?)`, base, contract.ChartViews).Scan(&document, &managed, &receipt, &raw, &graph)
	if err != nil {
		return contract.ChartView{}, ui.Component{}, "", agent.ErrNotFound
	}
	// Retain the existing accepted-normalizer/privacy boundary. Forget clears
	// that receipt; a retained view must not make revoked UI visible again.
	if _, _, err = storedUI(document, managed, receipt); err != nil {
		return contract.ChartView{}, ui.Component{}, "", agent.ErrStorage
	}
	var result contract.ChartViewsResult
	if err = rsiplugin.DecodeResult(json.RawMessage(raw), &result); err != nil {
		return contract.ChartView{}, ui.Component{}, "", agent.ErrStorage
	}
	if err = validChartViews(document, result); err != nil {
		return contract.ChartView{}, ui.Component{}, "", agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component == i {
			w, err := managedUIDocument(document)
			if err != nil {
				return contract.ChartView{}, ui.Component{}, "", agent.ErrStorage
			}
			return view, w.Components[i], graph, nil
		}
	}
	return contract.ChartView{}, ui.Component{}, "", agent.ErrNotFound
}

const uiChartColumn = `CASE WHEN EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-chart-view/1') THEN coalesce((SELECT p.result_json FROM ui_chart_view_snapshot p JOIN rsi_run b ON b.run_id=w.run_id WHERE p.invocation_id=w.invocation_id AND p.graph_revision=b.graph_revision),'') ELSE NULL END`

func bindChartViewRefs(document, ref string, components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.ChartViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil || validChartViews(document, result) != nil {
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
