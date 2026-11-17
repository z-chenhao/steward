package intelligentui

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed view/charts-worker.js
var chartsWorker string

// Presentation policy remains a single chart-family contribution. The existing
// shared renderer supplies DOM/SVG drawing; this source never runs in its realm.
func chartViews(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.ChartInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 768<<10 {
		return rsiplugin.Result{}, errors.New("invalid chart facts input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.ChartFacts
	if rsiplugin.DecodeInput(data, &facts) != nil {
		return rsiplugin.JSONResult(ctx, contract.ChartViewsResult{Views: []contract.ChartView{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return rsiplugin.JSONResult(ctx, renderChartViews(facts))
}

func renderChartViews(facts contract.ChartFacts) contract.ChartViewsResult {
	invalid := contract.ChartViewsResult{Views: []contract.ChartView{}, ErrorCode: "INVALID_ARGUMENT"}
	if facts.Charts == nil || len(facts.Charts) > 16 {
		return invalid
	}
	result := contract.ChartViewsResult{Views: []contract.ChartView{}}
	seen := map[int]bool{}
	for _, fact := range facts.Charts {
		if fact.Component < 0 || fact.Component > 15 || seen[fact.Component] {
			return invalid
		}
		seen[fact.Component] = true
		switch fact.Name {
		case "barChart", "pieChart", "steward.lineChart", "steward.scatterChart":
		default:
			return invalid
		}
		if !validNative(Component{Name: fact.Name, Props: fact.Props}) {
			return invalid
		}
		var props struct {
			Title       string    `json:"title"`
			Description string    `json:"description"`
			Unit        string    `json:"unit"`
			XValues     []float64 `json:"xValues"`
			Series      []struct {
				Name string `json:"name"`
			} `json:"series"`
		}
		if json.Unmarshal(fact.Props, &props) != nil {
			return invalid
		}
		columns := []contract.ChartColumn{{Header: "Label", Source: "label"}}
		if fact.Name == "steward.scatterChart" {
			columns = append(columns, contract.ChartColumn{Header: "X", Source: "x"}, contract.ChartColumn{Header: "Y", Source: "y"})
		} else if len(props.Series) > 0 {
			if props.XValues != nil {
				columns = append(columns, contract.ChartColumn{Header: "X", Source: "axis_x"})
			}
			for i, series := range props.Series {
				header := series.Name
				if props.Unit != "" {
					header += " (" + props.Unit + ")"
				}
				index := i
				columns = append(columns, contract.ChartColumn{Header: header, Source: "series", Series: &index})
			}
		} else {
			columns = append(columns, contract.ChartColumn{Header: "Value", Source: "value"})
		}
		result.Views = append(result.Views, contract.ChartView{Component: fact.Component, Worker: chartsWorker, Title: props.Title, Description: props.Description, Unit: props.Unit, Columns: columns})
	}
	return result
}
