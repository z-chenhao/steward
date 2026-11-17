package contract

import "encoding/json"

// FormViews is a private contribution for the existing steward.form renderer.
// It shares the UI package lifecycle; it is not a general form or renderer SDK.
const FormViews = "steward.ui-form-view/1"
const FormViewInput = "ui-form-views"

type FormViewFacts struct {
	Forms []FormViewFact `json:"forms"`
}
type FormViewFact struct {
	Component int             `json:"component"`
	Name      string          `json:"name"`
	Props     json.RawMessage `json:"props"`
}
type FormViewOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type FormViewField struct {
	Name     string           `json:"name"`
	Label    string           `json:"label"`
	Type     string           `json:"type"`
	Default  string           `json:"default"`
	Required bool             `json:"required,omitempty"`
	Options  []FormViewOption `json:"options,omitempty"`
	Min      *float64         `json:"min,omitempty"`
	Max      *float64         `json:"max,omitempty"`
}
type FormView struct {
	Component   int             `json:"component"`
	Title       string          `json:"title"`
	SubmitLabel string          `json:"submit_label"`
	Fields      []FormViewField `json:"fields"`
}
type FormViewsResult struct {
	Views     []FormView `json:"views"`
	ErrorCode string     `json:"error_code"`
}

const FormViewsInputSchema = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
const formViewFieldSchema = `{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":64},"label":{"type":"string","minLength":1,"maxLength":256},"type":{"type":"string","enum":["text","textarea","number","date","email","select","multiselect","boolean"]},"default":{"type":"string","maxLength":4096},"required":{"type":"boolean"},"options":{"type":"array","maxItems":32,"items":{"type":"object","properties":{"value":{"type":"string","minLength":1,"maxLength":200},"label":{"type":"string","minLength":1,"maxLength":200}},"required":["value","label"],"additionalProperties":false}},"min":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000},"max":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["name","label","type","default"],"additionalProperties":false}`
const FormViewsOutputSchema = `{"type":"object","properties":{"views":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"component":{"type":"integer","minimum":0,"maximum":15},"title":{"type":"string","minLength":1,"maxLength":300},"submit_label":{"type":"string","maxLength":100},"fields":{"type":"array","minItems":1,"maxItems":16,"items":` + formViewFieldSchema + `}},"required":["component","title","submit_label","fields"],"additionalProperties":false}},"error_code":{"type":"string","enum":["","INVALID_ARGUMENT"]}},"required":["views","error_code"],"additionalProperties":false}`
