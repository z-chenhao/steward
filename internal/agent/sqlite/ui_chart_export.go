package sqlite

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

type originalChartProps struct {
	Title string `json:"title"`
	Data  []struct {
		Label string   `json:"label"`
		Value *float64 `json:"value"`
		X     *float64 `json:"x"`
		Y     *float64 `json:"y"`
	} `json:"data"`
	Labels  []string  `json:"labels"`
	XValues []float64 `json:"xValues"`
	Series  []struct {
		Name   string    `json:"name"`
		Values []float64 `json:"values"`
	} `json:"series"`
}

// Columns are the accepted owner's layout; cells always come from original
// immutable widget facts. Neither browser nor Worker supplies download bytes.
func chartExportFacts(component ui.Component, view contract.ChartView, rows []int) (ui.ChartExport, error) {
	var props originalChartProps
	if json.Unmarshal(component.Props, &props) != nil || rows == nil || len(rows) > 200 || len(view.Columns) < 1 || len(view.Columns) > 8 {
		return ui.ChartExport{}, agent.ErrNotFound
	}
	count := len(props.Labels)
	if props.Data != nil {
		count = len(props.Data)
	}
	result := ui.ChartExport{Title: view.Title, Columns: []string{}, Rows: [][]json.RawMessage{}}
	for _, column := range view.Columns {
		valid := false
		switch column.Source {
		case "label":
			valid = true
		case "value":
			valid = component.Name != "steward.scatterChart" && props.Data != nil
		case "x", "y":
			valid = component.Name == "steward.scatterChart"
		case "axis_x":
			valid = component.Name == "steward.lineChart" && props.XValues != nil && len(props.XValues) == count
		case "series":
			valid = component.Name == "steward.lineChart" && column.Series != nil && *column.Series >= 0 && *column.Series < len(props.Series) && len(props.Series[*column.Series].Values) == count
		}
		if !valid || column.Source != "series" && column.Series != nil {
			return ui.ChartExport{}, agent.ErrNotFound
		}
		result.Columns = append(result.Columns, column.Header)
	}
	seen := map[int]bool{}
	for _, index := range rows {
		if index < 0 || index >= count || seen[index] {
			return ui.ChartExport{}, agent.ErrNotFound
		}
		seen[index] = true
		cells := []json.RawMessage{}
		for _, column := range view.Columns {
			var cell json.RawMessage
			var number *float64
			switch column.Source {
			case "label":
				label := ""
				if props.Data != nil {
					label = props.Data[index].Label
				} else {
					label = props.Labels[index]
				}
				cell, _ = json.Marshal(label)
			case "value":
				number = props.Data[index].Value
			case "x":
				number = props.Data[index].X
			case "y":
				number = props.Data[index].Y
			case "axis_x":
				number = &props.XValues[index]
			case "series":
				number = &props.Series[*column.Series].Values[index]
			}
			if cell == nil {
				if number == nil {
					return ui.ChartExport{}, agent.ErrNotFound
				}
				cell, _ = json.Marshal(*number)
			}
			cells = append(cells, cell)
		}
		result.Rows = append(result.Rows, cells)
	}
	return result, nil
}
func (s *Store) WebChartExportSource(ctx context.Context, ref string, rows []int) (ui.ChartExport, string, error) {
	view, component, graph, err := s.WebChartViewSource(ctx, ref)
	if err != nil {
		return ui.ChartExport{}, "", err
	}
	result, err := chartExportFacts(component, view, rows)
	return result, graph, err
}
