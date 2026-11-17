package intelligentui

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"testing"
)

func TestUIChartFamilyContributionPreservesOriginalSlots(t *testing.T) {
	facts := contract.ChartFacts{Charts: []contract.ChartFact{}}
	for index, name := range []string{"barChart", "pieChart", "steward.lineChart", "steward.scatterChart"} {
		props := json.RawMessage(`{"title":"Values","description":"Original","data":[{"label":"A","value":2}]}`)
		if name == "steward.scatterChart" {
			props = json.RawMessage(`{"title":"Values","description":"Original","data":[{"label":"A","x":1,"y":2}]}`)
		}
		facts.Charts = append(facts.Charts, contract.ChartFact{Component: index + 2, Name: name, Props: props})
	}
	result := renderChartViews(facts)
	if result.ErrorCode != "" || len(result.Views) != 4 {
		t.Fatal("chart contribution rejected", result.ErrorCode)
	}
	for i, view := range result.Views {
		if view.Component != i+2 || view.Worker != chartsWorker || view.Worker == "" {
			t.Fatal("slot/policy changed", view.Component)
		}
	}
	raw, _ := json.Marshal(result)
	if !tool.Validate(contract.ChartViewsOutputSchema, string(raw)) {
		t.Fatal("invalid contribution schema")
	}
}
func TestUIChartFamilyContributionRejectsInvalidWithoutPartialResults(t *testing.T) {
	good := contract.ChartFact{Component: 1, Name: "barChart", Props: json.RawMessage(`{"title":"Values","description":"Original","data":[{"label":"A","value":2}]}`)}
	for _, bad := range []contract.ChartFact{{Component: -1, Name: good.Name, Props: good.Props}, {Component: 16, Name: good.Name, Props: good.Props}, {Component: 2, Name: "Table", Props: good.Props}, {Component: 2, Name: good.Name, Props: json.RawMessage(`{}`)}, good} {
		got := renderChartViews(contract.ChartFacts{Charts: []contract.ChartFact{good, bad}})
		if got.ErrorCode != "INVALID_ARGUMENT" || got.Views == nil || len(got.Views) != 0 {
			t.Fatal("invalid chart released partial policy", got.ErrorCode)
		}
	}
}

func TestUIChartNumericSeriesOwnsFrozenCSVHeaders(t *testing.T) {
	result := renderChartViews(contract.ChartFacts{Charts: []contract.ChartFact{{Component: 0, Name: "steward.lineChart", Props: json.RawMessage(`{"title":"Price","description":"Original","unit":"元","labels":["0","1000"],"xValues":[0,1000],"series":[{"name":"A","values":[99,179]},{"name":"B","values":[299,329]}]}`)}}})
	if result.ErrorCode != "" || len(result.Views) != 1 {
		t.Fatal("series rejected")
	}
	view := result.Views[0]
	if view.Title != "Price" || view.Unit != "元" || len(view.Columns) != 4 || view.Columns[1].Source != "axis_x" || view.Columns[3].Header != "B (元)" || view.Columns[3].Series == nil || *view.Columns[3].Series != 1 {
		t.Fatal("owning CSV layout changed", view.Columns)
	}
}
