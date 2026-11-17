package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

const formViewDocument = `{"title":"Views","fallback":"Original form","components":[{"name":"plan_visualization","props":{"approach":"Keep facts","technology":"Go","key_elements":[]}},{"name":"steward.form","props":{"title":"Original","fields":[{"name":"mode","label":"Mode","type":"select","required":true,"options":[{"value":"train","label":"Train"}]},{"name":"extras","label":"Extras","type":"multiselect","options":[{"value":"meal","label":"Meal"}]}]}}]}`

func formViewReceipt() contract.FormViewsResult {
	return contract.FormViewsResult{Views: []contract.FormView{{Component: 1, Title: "Owning title", SubmitLabel: "Continue", Fields: []contract.FormViewField{
		{Name: "extras", Label: "Owning extras", Type: "multiselect", Default: `["meal"]`, Options: []contract.FormViewOption{{Value: "meal", Label: "Owning meal"}}},
		{Name: "mode", Label: "Owning mode", Type: "select", Required: true, Default: "train", Options: []contract.FormViewOption{{Value: "train", Label: "Owning train"}}},
	}}}}
}

func TestUIFormViewAcceptanceBindsOriginalFieldsWithoutFixingDisplayPolicy(t *testing.T) {
	if err := validFormViews(formViewDocument, formViewReceipt()); err != nil {
		t.Fatal("independent field order/labels/defaults rejected", err)
	}
	for name, change := range map[string]func(*contract.FormViewsResult){
		"missing form":              func(v *contract.FormViewsResult) { v.Views = v.Views[:0] },
		"nil batch":                 func(v *contract.FormViewsResult) { v.Views = nil },
		"duplicate form":            func(v *contract.FormViewsResult) { v.Views = append(v.Views, v.Views[0]) },
		"foreign slot":              func(v *contract.FormViewsResult) { v.Views[0].Component = 0 },
		"missing field":             func(v *contract.FormViewsResult) { v.Views[0].Fields = v.Views[0].Fields[:1] },
		"duplicate field":           func(v *contract.FormViewsResult) { v.Views[0].Fields[1] = v.Views[0].Fields[0] },
		"foreign field":             func(v *contract.FormViewsResult) { v.Views[0].Fields[0].Name = "invented" },
		"foreign control":           func(v *contract.FormViewsResult) { v.Views[0].Fields[0].Type = "text" },
		"required constraint":       func(v *contract.FormViewsResult) { v.Views[0].Fields[1].Required = false },
		"foreign option":            func(v *contract.FormViewsResult) { v.Views[0].Fields[0].Options[0].Value = "invented" },
		"foreign initial selection": func(v *contract.FormViewsResult) { v.Views[0].Fields[1].Default = "invented" },
		"malformed multiselect":     func(v *contract.FormViewsResult) { v.Views[0].Fields[0].Default = "null" },
		"duplicate multiselect":     func(v *contract.FormViewsResult) { v.Views[0].Fields[0].Default = `["meal","meal"]` },
	} {
		t.Run(name, func(t *testing.T) {
			view := formViewReceipt()
			change(&view)
			if err := validFormViews(formViewDocument, view); err == nil {
				t.Fatal("accepted changed original form contract")
			}
		})
	}
}

func TestUIFormViewReferenceBindingKeepsHistoricalBoundaryAndFormIdentity(t *testing.T) {
	components := []ui.Component{{Name: "plan_visualization"}, {Name: "steward.form", FormID: "form-v2-original", WidgetRef: "ui:original#1"}}
	if err := bindFormViewRefs(formViewDocument, "ui:original", components, sql.NullString{}); err != nil || components[1].ViewRef != "" {
		t.Fatal("historical view was invented", err)
	}
	if err := bindFormViewRefs(formViewDocument, "ui:original", components, sql.NullString{Valid: true}); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("missing managed receipt took native fallback", err)
	}
	raw, _ := json.Marshal(formViewReceipt())
	if err := bindFormViewRefs(formViewDocument, "ui:original", components, sql.NullString{Valid: true, String: string(raw)}); err != nil {
		t.Fatal(err)
	}
	if components[1].ViewRef != "ui:original#1" || components[1].WidgetRef != "ui:original#1" || components[1].FormID != "form-v2-original" || components[0].ViewRef != "" {
		t.Fatal("form identity or original ordinal changed", components)
	}
}
