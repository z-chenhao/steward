package intelligentui

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The existing native controls remain a fixed driver. This producer owns form
// presentation and initial values, without DOM, code, ingress or effect grants.
func formViews(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.FormViewInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 128<<10 {
		return rsiplugin.Result{}, errors.New("invalid form view facts input")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.FormViewFacts
	if rsiplugin.DecodeInput(raw, &facts) != nil {
		return rsiplugin.JSONResult(ctx, contract.FormViewsResult{Views: []contract.FormView{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return rsiplugin.JSONResult(ctx, renderFormViews(facts))
}

func renderFormViews(facts contract.FormViewFacts) contract.FormViewsResult {
	invalid := contract.FormViewsResult{Views: []contract.FormView{}, ErrorCode: "INVALID_ARGUMENT"}
	if facts.Forms == nil || len(facts.Forms) > 16 {
		return invalid
	}
	result := contract.FormViewsResult{Views: []contract.FormView{}}
	seen := map[int]bool{}
	for _, fact := range facts.Forms {
		if fact.Component < 0 || fact.Component > 15 || seen[fact.Component] || fact.Name != "steward.form" || !validNative(Component{Name: fact.Name, Props: fact.Props}) {
			return invalid
		}
		seen[fact.Component] = true
		var props FormProps
		if json.Unmarshal(fact.Props, &props) != nil {
			return invalid
		}
		view := contract.FormView{Component: fact.Component, Title: props.Title, SubmitLabel: props.SubmitLabel, Fields: []contract.FormViewField{}}
		for _, field := range props.Fields {
			value := ""
			switch field.Type {
			case "boolean":
				value = "false"
			case "multiselect":
				value = "[]"
			}
			item := contract.FormViewField{Name: field.Name, Label: field.Label, Type: field.Type, Default: value, Required: field.Required, Min: field.Min, Max: field.Max}
			for _, option := range field.Options {
				item.Options = append(item.Options, contract.FormViewOption{Value: option.Value, Label: option.Label})
			}
			view.Fields = append(view.Fields, item)
		}
		result.Views = append(result.Views, view)
	}
	return result
}
