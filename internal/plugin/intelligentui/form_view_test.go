package intelligentui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestUIFormViewPreservesFieldsAndOriginalDraftDefaults(t *testing.T) {
	props := json.RawMessage(`{"title":"职业规划\r\n","submitLabel":"确认","fields":[{"name":"name","label":"姓名","type":"text","required":true},{"name":"notes","label":"Notes","type":"textarea"},{"name":"count","label":"Count","type":"number","min":1,"max":5},{"name":"date","label":"Date","type":"date"},{"name":"email","label":"Email","type":"email"},{"name":"mode","label":"Mode","type":"select","options":[{"value":"train","label":"Train"}]},{"name":"extras","label":"Extras","type":"multiselect","options":[{"value":"meal","label":"Meal"}]},{"name":"confirm","label":"Confirm","type":"boolean","required":true}]}`)
	original := string(props)
	result := renderFormViews(contract.FormViewFacts{Forms: []contract.FormViewFact{{Component: 3, Name: "steward.form", Props: props}}})
	if result.ErrorCode != "" || len(result.Views) != 1 {
		t.Fatal("original form rejected", result.ErrorCode)
	}
	view := result.Views[0]
	if view.Component != 3 || view.Title != "职业规划\r\n" || view.SubmitLabel != "确认" || len(view.Fields) != 8 || !view.Fields[0].Required || *view.Fields[2].Min != 1 || *view.Fields[2].Max != 5 {
		t.Fatal("original presentation changed", view)
	}
	var defaults []string
	for _, field := range view.Fields {
		defaults = append(defaults, field.Default)
	}
	if !reflect.DeepEqual(defaults, []string{"", "", "", "", "", "", "[]", "false"}) || view.Fields[5].Options[0].Value != "train" || view.Fields[6].Options[0].Label != "Meal" || string(props) != original {
		t.Fatal("draft values/options or original facts changed", defaults)
	}
	raw, _ := json.Marshal(result)
	if !tool.Validate(contract.FormViewsOutputSchema, string(raw)) {
		t.Fatal("form contribution violates private schema", string(raw))
	}
}

func TestUIFormViewRejectsInvalidWithoutPartialContributions(t *testing.T) {
	good := contract.FormViewFact{Component: 1, Name: "steward.form", Props: json.RawMessage(`{"title":"Original","fields":[{"name":"name","label":"Name","type":"text"}]}`)}
	for _, bad := range []contract.FormViewFact{good, {Component: -1, Name: good.Name, Props: good.Props}, {Component: 16, Name: good.Name, Props: good.Props}, {Component: 2, Name: "scheduleTime", Props: good.Props}, {Component: 2, Name: good.Name, Props: json.RawMessage(`{"title":"Invalid","fields":[{"name":"same","label":"A","type":"text"},{"name":"same","label":"B","type":"text"}]}`)}} {
		got := renderFormViews(contract.FormViewFacts{Forms: []contract.FormViewFact{good, bad}})
		if got.ErrorCode != "INVALID_ARGUMENT" || got.Views == nil || len(got.Views) != 0 {
			t.Fatal("invalid form released partial view", got)
		}
	}
	if got := renderFormViews(contract.FormViewFacts{Forms: []contract.FormViewFact{}}); got.ErrorCode != "" || got.Views == nil || len(got.Views) != 0 {
		t.Fatal("empty batch rejected", got)
	}
}
