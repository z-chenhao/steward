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

func validTableViews(document string, result contract.TableViewsResult) error {
	w, err := managedUIDocument(document)
	if err != nil || result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	expected := map[int]int{}
	for i, c := range w.Components {
		if c.Name == "Table" {
			var props struct {
				Columns []string `json:"columns"`
			}
			if json.Unmarshal(c.Props, &props) != nil {
				return rsi.ErrInvalid
			}
			expected[i] = len(props.Columns)
		}
	}
	return validTableViewSlots(expected, result)
}

func validTableViewSlots(expected map[int]int, result contract.TableViewsResult) error {
	if result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 || len(result.Views) != len(expected) {
		return rsi.ErrInvalid
	}
	for _, v := range result.Views {
		columns := expected[v.Component]
		if columns < 1 || columns > 12 || v.CompactMaxWidth < 1 || v.CompactMaxWidth > 4096 || len(v.Contexts) != 2*(columns+1) {
			return rsi.ErrInvalid
		}
		for _, text := range []struct {
			Value string
			Limit int
		}{{v.HTML, 512 << 10}, {v.Worker, 16 << 10}, {v.CSS, 16 << 10}} {
			if text.Value == "" || len(text.Value) > text.Limit || !utf8.ValidString(text.Value) || strings.ContainsRune(text.Value, 0) {
				return rsi.ErrInvalid
			}
		}
		seen := map[[2]int]bool{}
		for _, c := range v.Contexts {
			key := [2]int{c.Sort, c.Direction}
			if c.Sort < -1 || c.Sort >= columns || (c.Direction != 1 && c.Direction != -1) || seen[key] || c.Description.Direction != c.Direction || !utf8.ValidString(c.Description.Title) || utf8.RuneCountInString(c.Description.Title) > 300 {
				return rsi.ErrInvalid
			}
			if c.Sort == -1 {
				if c.Description.Sort != nil {
					return rsi.ErrInvalid
				}
			} else if c.Description.Sort == nil || !utf8.ValidString(*c.Description.Sort) || utf8.RuneCountInString(*c.Description.Sort) > 200 {
				return rsi.ErrInvalid
			}
			// This is frozen text, not an accepted UI state. Original NUL title/column
			// strings remain data; the existing state API still independently rejects
			// invalid model_context when a user later proposes a sort, just as before.
			seen[key] = true
		}
		delete(expected, v.Component)
	}
	return nil
}

// WebTableViewSource reads a delivered, account-owned component's accepted pure
// output. No candidate, active selection, credential or arbitrary path is read.
func (s *Store) WebTableViewSource(ctx context.Context, ref string) (contract.TableView, string, error) {
	view, _, graph, err := s.tableViewSource(ctx, ref)
	return view, graph, err
}

// WebTableExportSource grants only original row positions from an accepted Table.
// Candidate-provided cell values, titles, destinations and bytes are never used.
func (s *Store) WebTableExportSource(ctx context.Context, ref string, rows []int) (ui.TableExport, string, error) {
	view, document, graph, err := s.tableViewSource(ctx, ref)
	if err != nil {
		return ui.TableExport{}, "", err
	}
	result, err := tableExportRows(document, view.Component, rows)
	return result, graph, err
}

func tableExportRows(document string, component int, rows []int) (ui.TableExport, error) {
	w, err := managedUIDocument(document)
	if err != nil || component < 0 || component >= len(w.Components) || w.Components[component].Name != "Table" || rows == nil || len(rows) > 200 {
		return ui.TableExport{}, agent.ErrNotFound
	}
	var original ui.TableExport
	if json.Unmarshal(w.Components[component].Props, &original) != nil {
		return ui.TableExport{}, agent.ErrStorage
	}
	result := ui.TableExport{Title: original.Title, Columns: original.Columns, Rows: make([][]string, 0, len(rows))}
	seen := map[int]bool{}
	for _, row := range rows {
		if row < 0 || row >= len(original.Rows) || seen[row] {
			return ui.TableExport{}, agent.ErrNotFound
		}
		seen[row] = true
		result.Rows = append(result.Rows, original.Rows[row])
	}
	return result, nil
}

func (s *Store) tableViewSource(ctx context.Context, ref string) (contract.TableView, string, string, error) {
	base, index, ok := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return contract.TableView{}, "", "", agent.ErrNotFound
	}
	var document, receipt, raw, graph string
	var managed bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_table_view_snapshot p USING(invocation_id) JOIN run r USING(run_id) JOIN rsi_run b USING(run_id) JOIN tool_invocation t USING(invocation_id) JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded' AND p.graph_revision=b.graph_revision AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value=?)`, base, contract.TableViews).Scan(&document, &managed, &receipt, &raw, &graph)
	if err != nil {
		return contract.TableView{}, "", "", agent.ErrNotFound
	}
	// Retain the existing accepted-normalizer/privacy boundary. Forget clears
	// that receipt; a retained view must not make revoked UI visible again.
	if _, _, err = storedUI(document, managed, receipt); err != nil {
		return contract.TableView{}, "", "", agent.ErrStorage
	}
	var result contract.TableViewsResult
	if err = rsiplugin.DecodeResult(json.RawMessage(raw), &result); err != nil {
		return contract.TableView{}, "", "", agent.ErrStorage
	}
	if err = validTableViews(document, result); err != nil {
		return contract.TableView{}, "", "", agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component == i {
			return view, document, graph, nil
		}
	}
	return contract.TableView{}, "", "", agent.ErrNotFound
}

// NULL is an explicit historical boundary. Empty managed receipts are corrupt,
// not a reason to render the current native implementation.
const uiTableColumn = `CASE WHEN EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-table-view/1') THEN coalesce((SELECT p.result_json FROM ui_table_view_snapshot p JOIN rsi_run b ON b.run_id=w.run_id WHERE p.invocation_id=w.invocation_id AND p.graph_revision=b.graph_revision),'') ELSE NULL END`

func bindTableViewRefs(document, ref string, components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.TableViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil || validTableViews(document, result) != nil {
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
