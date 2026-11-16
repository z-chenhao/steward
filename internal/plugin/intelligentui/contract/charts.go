package contract

import "encoding/json"

// ChartViews is the private chart-family contribution in the existing UI
// package. Host binds original component refs; Worker owns chart policy.
const ChartViews = "steward.ui-chart-view/1"
const ChartInput = "ui-charts"

type ChartFacts struct {
	Charts []ChartFact `json:"charts"`
}
type ChartFact struct {
	Component int             `json:"component"`
	Name      string          `json:"name"`
	Props     json.RawMessage `json:"props"`
}
type ChartColumn struct {
	Header string `json:"header"`
	Source string `json:"source"`
	Series *int   `json:"series,omitempty"`
}

type ChartView struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Unit        string        `json:"unit"`
	Columns     []ChartColumn `json:"columns"`
	Component   int           `json:"component"`
	Worker      string        `json:"worker"`
}
type ChartViewsResult struct {
	Views     []ChartView `json:"views"`
	ErrorCode string      `json:"error_code"`
}

const ChartViewsInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const ChartViewsOutputSchema = `{"type":"object","properties":{"views":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"component":{"type":"integer","minimum":0,"maximum":15},"worker":{"type":"string","minLength":1,"maxLength":16384},"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"unit":{"type":"string","maxLength":64},"columns":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","properties":{"header":{"type":"string","maxLength":300},"source":{"type":"string","enum":["label","value","x","y","axis_x","series"]},"series":{"type":"integer","minimum":0,"maximum":5}},"required":["header","source"],"additionalProperties":false}}},"required":["component","worker","title","description","unit","columns"],"additionalProperties":false}},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["views","error_code"],"additionalProperties":false}`
