package intelligentui

import (
	"encoding/json"
	"fmt"
	"strings"
)

func nativeSummary(c Component) string {
	if strings.HasPrefix(c.Name, "steward.") {
		return extensionSummary(c)
	}
	var p nativeProps
	if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	lines := []string{p.Title}
	switch c.Name {
	case "Table":
		lines = append(lines, strings.Join(p.Columns, " | "))
		for _, r := range p.Rows {
			lines = append(lines, strings.Join(r, " | "))
		}
		lines = append(lines, p.Source)
	case "barChart", "pieChart":
		lines = append(lines, p.Description)
		for _, d := range p.Data {
			lines = append(lines, fmt.Sprintf("%s: %g", d.Label, d.Value))
		}
	case "plan_visualization":
		lines = []string{p.Technology, p.Approach}
		lines = append(lines, p.KeyElements...)
	case "scheduleTime":
		lines = append(lines, p.Reason)
		for _, slot := range p.TimeSlots {
			lines = append(lines, fmt.Sprintf("%s %s (%g min)", slot.Date, slot.Time, p.Duration))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extensionSummary(c Component) string {
	var p struct {
		Title, Description, Label string
		Fields                    []FormField
		Data                      []struct {
			Label       string
			Value, X, Y float64
		}
		Items []struct{ Title, Text string }
	}
	if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	lines := []string{p.Title, p.Description, p.Label}
	if c.Name == "steward.lineChart" {
		var chart LineProps
		_ = json.Unmarshal(c.Props, &chart)
		for _, series := range chart.Series {
			for i, value := range series.Values {
				label := chart.Labels[i]
				if chart.XValues != nil {
					label = fmt.Sprintf("%s (x=%g)", label, chart.XValues[i])
				}
				lines = append(lines, fmt.Sprintf("%s / %s: %g %s", series.Name, label, value, chart.Unit))
			}
		}
	}
	for _, f := range p.Fields {
		lines = append(lines, f.Label)
	}
	for _, d := range p.Data {
		if c.Name == "steward.scatterChart" {
			lines = append(lines, fmt.Sprintf("%s: %g, %g", d.Label, d.X, d.Y))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %g", d.Label, d.Value))
		}
	}
	for _, item := range p.Items {
		lines = append(lines, item.Title, item.Text)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
