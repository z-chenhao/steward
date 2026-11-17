package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"reflect"
	"strconv"
	"strings"
)

func (s *Store) WebFormViewSource(ctx context.Context, ref string) (contract.FormView, ui.Component, string, error) {
	base, index, ok := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return contract.FormView{}, ui.Component{}, "", agent.ErrNotFound
	}
	var document, receipt, raw, graph string
	var managed bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_form_view_snapshot p USING(invocation_id) JOIN run r USING(run_id) JOIN rsi_run b USING(run_id) JOIN tool_invocation t USING(invocation_id) JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded' AND p.graph_revision=b.graph_revision AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value=?)`, base, contract.FormViews).Scan(&document, &managed, &receipt, &raw, &graph)
	if err != nil {
		return contract.FormView{}, ui.Component{}, "", agent.ErrNotFound
	}
	// Retain the existing accepted-normalizer/privacy boundary. Forget clears
	// that receipt; a retained view must not make revoked UI visible again.
	if _, _, err = storedUI(document, managed, receipt); err != nil {
		return contract.FormView{}, ui.Component{}, "", agent.ErrStorage
	}
	var result contract.FormViewsResult
	if err = rsiplugin.DecodeResult(json.RawMessage(raw), &result); err != nil {
		return contract.FormView{}, ui.Component{}, "", agent.ErrStorage
	}
	if err = validFormViews(document, result); err != nil {
		return contract.FormView{}, ui.Component{}, "", agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component == i {
			w, err := managedUIDocument(document)
			if err != nil {
				return contract.FormView{}, ui.Component{}, "", agent.ErrStorage
			}
			return view, w.Components[i], graph, nil
		}
	}
	return contract.FormView{}, ui.Component{}, "", agent.ErrNotFound
}

const uiFormViewColumn = `CASE WHEN EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-form-view/1') THEN coalesce((SELECT p.result_json FROM ui_form_view_snapshot p JOIN rsi_run b ON b.run_id=w.run_id WHERE p.invocation_id=w.invocation_id AND p.graph_revision=b.graph_revision),'') ELSE NULL END`

func bindFormViewRefs(document, ref string, components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.FormViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil || validFormViews(document, result) != nil {
		return agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component >= len(components) {
			return agent.ErrStorage
		}
		components[view.Component].ViewRef = ref + "#" + strconv.Itoa(view.Component)
	}
	return nil
}

// Keep original field/option identities and constraints. Display labels/order
// and initial values are owning policy, never a grant to add form arguments.
func validFormViews(document string, result contract.FormViewsResult) error {
	w, err := managedUIDocument(document)
	raw, encodeErr := json.Marshal(result)
	if err != nil || encodeErr != nil || !tool.Validate(contract.FormViewsOutputSchema, string(raw)) || result.ErrorCode != "" {
		return rsi.ErrInvalid
	}
	expected := map[int]ui.Component{}
	for i, c := range w.Components {
		if c.Name == "steward.form" {
			expected[i] = c
		}
	}
	if len(expected) != len(result.Views) {
		return rsi.ErrInvalid
	}
	for _, view := range result.Views {
		component, ok := expected[view.Component]
		if !ok {
			return rsi.ErrInvalid
		}
		var props struct {
			Fields []contract.FormViewField `json:"fields"`
		}
		if json.Unmarshal(component.Props, &props) != nil || len(props.Fields) != len(view.Fields) {
			return rsi.ErrInvalid
		}
		original := map[string]contract.FormViewField{}
		for _, field := range props.Fields {
			original[field.Name] = field
		}
		for _, field := range view.Fields {
			before, ok := original[field.Name]
			if !ok || field.Type != before.Type || field.Required != before.Required || !reflect.DeepEqual(field.Min, before.Min) || !reflect.DeepEqual(field.Max, before.Max) || len(field.Options) != len(before.Options) {
				return rsi.ErrInvalid
			}
			options := map[string]bool{}
			for _, option := range before.Options {
				options[option.Value] = true
			}
			seen := map[string]bool{}
			for _, option := range field.Options {
				if !options[option.Value] || seen[option.Value] {
					return rsi.ErrInvalid
				}
				seen[option.Value] = true
			}
			switch field.Type {
			case "boolean":
				if field.Default != "true" && field.Default != "false" {
					return rsi.ErrInvalid
				}
			case "select":
				if field.Default != "" && !options[field.Default] {
					return rsi.ErrInvalid
				}
			case "multiselect":
				var values []string
				if json.Unmarshal([]byte(field.Default), &values) != nil || values == nil || len(values) > len(options) {
					return rsi.ErrInvalid
				}
				selected := map[string]bool{}
				for _, value := range values {
					if !options[value] || selected[value] {
						return rsi.ErrInvalid
					}
					selected[value] = true
				}
			}
			delete(original, field.Name)
		}
		delete(expected, view.Component)
	}
	return nil
}

// Static SSR display is attached only after the original projection/ordinal
// lineage matches. The pointer is not serialized or included in FormID hashes.
func bindFormViewDisplay(components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.FormViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil {
		return agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component < 0 || view.Component >= len(components) || components[view.Component].ViewRef == "" {
			return agent.ErrStorage
		}
		display := ui.FormView{Props: ui.FormProps{Title: view.Title, SubmitLabel: view.SubmitLabel, Fields: []ui.FormField{}}, Defaults: map[string]string{}}
		for _, field := range view.Fields {
			item := ui.FormField{Name: field.Name, Label: field.Label, Type: field.Type, Required: field.Required, Min: field.Min, Max: field.Max}
			for _, option := range field.Options {
				item.Options = append(item.Options, ui.FormOption{Value: option.Value, Label: option.Label})
			}
			display.Props.Fields = append(display.Props.Fields, item)
			display.Defaults[field.Name] = field.Default
		}
		components[view.Component].FormView = &display
	}
	return nil
}
