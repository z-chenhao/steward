package sqlite

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func TestUIChartExportOriginalNumericFactsAndOwningColumnOrder(t *testing.T) {
	component := ui.Component{Name: "steward.lineChart", Props: json.RawMessage(`{"title":"Original","unit":"元","labels":["0","1000","10000"],"xValues":[0,1000,10000],"series":[{"name":"A","values":[99,179,899]},{"name":"B","values":[299,329,599]}]}`)}
	index := 1
	view := contract.ChartView{Title: "Owner title", Columns: []contract.ChartColumn{{Header: "B (元)", Source: "series", Series: &index}, {Header: "X", Source: "axis_x"}, {Header: "Label", Source: "label"}}}
	result, err := chartExportFacts(component, view, []int{2, 0})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	expected := `{"title":"Owner title","columns":["B (元)","X","Label"],"rows":[[599,10000,"10000"],[299,0,"0"]]}`
	if string(raw) != expected {
		t.Fatalf("original numeric facts changed: %s", raw)
	}
	empty, err := chartExportFacts(component, view, []int{})
	if err != nil || empty.Rows == nil || len(empty.Rows) != 0 || !reflect.DeepEqual(empty.Columns, result.Columns) {
		t.Fatal("empty export lost owning columns", err)
	}
	for _, rows := range [][]int{nil, {-1}, {3}, {0, 0}} {
		if _, err = chartExportFacts(component, view, rows); err == nil {
			t.Fatal("invalid original positions accepted", rows)
		}
	}
	for _, column := range []contract.ChartColumn{{Header: "bad", Source: "value"}, {Header: "bad", Source: "series"}, {Header: "bad", Source: "label", Series: &index}, {Header: "bad", Source: "made-up"}} {
		bad := view
		bad.Columns = []contract.ChartColumn{column}
		if _, err = chartExportFacts(component, bad, []int{}); err == nil {
			t.Fatal("unbound column source accepted", column.Source)
		}
	}
}
func TestUIChartExportPreservesRawLabelAndScatterNumbers(t *testing.T) {
	component := ui.Component{Name: "steward.scatterChart", Props: json.RawMessage(`{"title":"Facts","description":"Original","data":[{"label":"Choice\r\nline\u0000中文","x":-1,"y":0}]}`)}
	view := contract.ChartView{Title: "Facts", Columns: []contract.ChartColumn{{Header: "Label", Source: "label"}, {Header: "X", Source: "x"}, {Header: "Y", Source: "y"}}}
	result, err := chartExportFacts(component, view, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	var label string
	if err = json.Unmarshal(result.Rows[0][0], &label); err != nil || label != "Choice\r\nline\x00中文" {
		t.Fatal("original label normalized", err)
	}
	if string(result.Rows[0][1]) != "-1" || string(result.Rows[0][2]) != "0" {
		t.Fatal("scatter values changed")
	}
}
