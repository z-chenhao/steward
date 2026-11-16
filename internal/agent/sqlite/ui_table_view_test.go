package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

const tableSlotDocument = `{"title":"Views","fallback":"Original values","components":[{"name":"plan_visualization","props":{"approach":"Keep facts","technology":"Go","key_elements":[]}},{"name":"Table","props":{"title":"Values","columns":["N"],"rows":[["42"]],"source":"Fixture"}}]}`

// This fixture tests Host acceptance, not candidate rendering or real dispatch.
func tableSlotResult() contract.TableViewsResult {
	name := "N"
	contexts := []contract.TableSortContext{}
	for column := -1; column < 1; column++ {
		for _, direction := range []int{1, -1} {
			var sort *string
			if column >= 0 {
				sort = &name
			}
			contexts = append(contexts, contract.TableSortContext{Sort: column, Direction: direction, Description: contract.TableDescription{Title: "Values", Sort: sort, Direction: direction}})
		}
	}
	return contract.TableViewsResult{Views: []contract.TableView{{Component: 1, HTML: "<table></table>", CSS: "table{color:inherit}", Worker: "/* owning fixture */", CompactMaxWidth: 760, Contexts: contexts}}}
}

func TestUITableViewAcceptanceRejectsMissingDuplicateAndForeignSlots(t *testing.T) {
	if err := validTableViews(tableSlotDocument, tableSlotResult()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*contract.TableViewsResult){
		"missing":               func(v *contract.TableViewsResult) { v.Views = v.Views[:0] },
		"nil":                   func(v *contract.TableViewsResult) { v.Views = nil },
		"duplicate":             func(v *contract.TableViewsResult) { v.Views = append(v.Views, v.Views[0]) },
		"other component":       func(v *contract.TableViewsResult) { v.Views[0].Component = 0 },
		"outside":               func(v *contract.TableViewsResult) { v.Views[0].Component = 16 },
		"missing description":   func(v *contract.TableViewsResult) { v.Views[0].Contexts = v.Views[0].Contexts[:3] },
		"duplicate state":       func(v *contract.TableViewsResult) { v.Views[0].Contexts[3] = v.Views[0].Contexts[2] },
		"foreign column":        func(v *contract.TableViewsResult) { v.Views[0].Contexts[2].Sort = 1 },
		"wrong direction":       func(v *contract.TableViewsResult) { v.Views[0].Contexts[0].Direction = 0 },
		"description direction": func(v *contract.TableViewsResult) { v.Views[0].Contexts[0].Description.Direction = -1 },
		"unsorted label": func(v *contract.TableViewsResult) {
			label := "invented"
			v.Views[0].Contexts[0].Description.Sort = &label
		},
		"missing label": func(v *contract.TableViewsResult) { v.Views[0].Contexts[2].Description.Sort = nil },
		"long title": func(v *contract.TableViewsResult) {
			v.Views[0].Contexts[0].Description.Title = strings.Repeat("x", 301)
		},
		"html budget":   func(v *contract.TableViewsResult) { v.Views[0].HTML = strings.Repeat("x", (512<<10)+1) },
		"worker budget": func(v *contract.TableViewsResult) { v.Views[0].Worker = strings.Repeat("x", (16<<10)+1) },
		"css budget":    func(v *contract.TableViewsResult) { v.Views[0].CSS = strings.Repeat("x", (16<<10)+1) },
		"nul html":      func(v *contract.TableViewsResult) { v.Views[0].HTML = "\x00" },
		"invalid utf8":  func(v *contract.TableViewsResult) { v.Views[0].CSS = string([]byte{255}) },
		"empty worker":  func(v *contract.TableViewsResult) { v.Views[0].Worker = "" },
		"breakpoint":    func(v *contract.TableViewsResult) { v.Views[0].CompactMaxWidth = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			v := tableSlotResult()
			change(&v)
			if err := validTableViews(tableSlotDocument, v); err == nil {
				t.Fatal("invalid candidate view accepted")
			}
		})
	}
}

func TestUITableViewBindingKeepsHistoricalNULLAndRejectsCorruptManagedReceipt(t *testing.T) {
	components := []ui.Component{{Name: "plan_visualization"}, {Name: "Table"}}
	if err := bindTableViewRefs(tableSlotDocument, "ui:original", components, sql.NullString{}); err != nil || components[1].ViewRef != "" {
		t.Fatal("historical NULL invented a managed view", err)
	}
	if err := bindTableViewRefs(tableSlotDocument, "ui:original", components, sql.NullString{Valid: true}); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("empty managed receipt used native boundary", err)
	}
	raw, _ := json.Marshal(tableSlotResult())
	if err := bindTableViewRefs(tableSlotDocument, "ui:original", components, sql.NullString{Valid: true, String: string(raw)}); err != nil || components[0].ViewRef != "" || components[1].ViewRef != "ui:original#1" {
		t.Fatal("lost original ordinal", components, err)
	}
}

func TestUITableHistoricalRunNeverInventsManagedReceipt(t *testing.T) {
	s, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	// Synthetic historical service graph, not the original archived UI1.13 binary.
	installUIRuntimeAtTableBoundary(t, s, runs, true)
	step := 0
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		step++
		if step == 1 {
			return calls(ui.ShowName, `{"title":"Historical","fallback":"Original","components":[{"name":"Table","props":{"title":"N","columns":["N"],"rows":[["42"]],"source":"Fixture"}},{"name":"steward.cards","props":{"items":[{"title":"A"}]}}]}`), nil
		}
		return llm.GenerationResult{Text: "Ready."}, nil
	}
	mustSubmit(t, actor, "historical-table", "Original service graph")
	mustDrain(t, actor)
	if scalar(t, s, `SELECT count(*) FROM ui_table_view_snapshot`) != 0 {
		t.Fatal("UI1.13 unexpectedly acquired Table snapshots")
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WebTableViewSource(context.Background(), ref+"#0"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("historical Table adopted managed view", err)
	}
	if _, _, err := s.WebCardsViewSource(context.Background(), ref+"#1"); err != nil {
		t.Fatal("Table migration broke current Cards publication", err)
	}
	for _, ref := range []string{ref + "#00", ref + "#-1", ref + "#1", "unknown#0"} {
		if _, _, err := s.WebTableViewSource(context.Background(), ref); !errors.Is(err, agent.ErrNotFound) {
			t.Fatal("foreign/noncanonical reference accepted", ref, err)
		}
	}
}

func TestUITableExportKeepsOriginalOrdinalValuesAndRejectsForeignRows(t *testing.T) {
	result, err := tableExportRows(tableSlotDocument, 1, []int{0})
	if err != nil || result.Title != "Values" || len(result.Columns) != 1 || result.Columns[0] != "N" || len(result.Rows) != 1 || result.Rows[0][0] != "42" {
		t.Fatal(result, err)
	}
	empty, err := tableExportRows(tableSlotDocument, 1, []int{})
	if err != nil || empty.Rows == nil || len(empty.Rows) != 0 {
		t.Fatal(empty, err)
	}
	for _, rows := range [][]int{nil, {-1}, {1}, {0, 0}} {
		if _, err := tableExportRows(tableSlotDocument, 1, rows); err == nil {
			t.Fatal("invalid row selection accepted", rows)
		}
	}
	for _, slot := range []int{-1, 0, 2} {
		if _, err := tableExportRows(tableSlotDocument, slot, []int{0}); err == nil {
			t.Fatal("foreign component accepted", slot)
		}
	}
}
