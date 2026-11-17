package app

import (
	"bytes"
	"context"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"reflect"
)

// Only the original accepted policy/facts are read; this path never executes a
// plugin or chooses a current version. Re-read after package I/O preserves the
// same revocation boundary used by other frozen UI contributions.
func (a *App) WebFormView(ctx context.Context, ref string) (ui.FormView, error) {
	accepted, facts, graph, err := a.store.WebFormViewSource(ctx, ref)
	if err != nil {
		return ui.FormView{}, err
	}
	host := a.Plugins()
	binding, err := host.ServiceInstance(ctx, graph, contract.FormViews)
	if err != nil {
		return ui.FormView{}, err
	}
	for _, service := range []string{contract.Normalize, contract.DefinitionContract, contract.FormContract} {
		original, e := host.ServiceInstance(ctx, graph, service)
		if e != nil {
			return ui.FormView{}, e
		}
		if original.InstanceID != binding.InstanceID || original.PackageDigest != binding.PackageDigest {
			return ui.FormView{}, rsi.ErrInvalid
		}
	}
	if _, err = host.Catalog.Load(binding.PackageDigest); err != nil {
		return ui.FormView{}, err
	}
	current, currentFacts, currentGraph, err := a.store.WebFormViewSource(ctx, ref)
	if err != nil {
		return ui.FormView{}, err
	}
	if currentGraph != graph || current.Component != accepted.Component || !reflect.DeepEqual(current, accepted) || currentFacts.Name != facts.Name || !bytes.Equal(currentFacts.Props, facts.Props) {
		return ui.FormView{}, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, graph); err != nil {
		return ui.FormView{}, err
	}
	view := ui.FormView{Props: ui.FormProps{Title: accepted.Title, SubmitLabel: accepted.SubmitLabel, Fields: []ui.FormField{}}, Defaults: map[string]string{}}
	for _, field := range accepted.Fields {
		item := ui.FormField{Name: field.Name, Label: field.Label, Type: field.Type, Required: field.Required, Min: field.Min, Max: field.Max}
		for _, option := range field.Options {
			item.Options = append(item.Options, ui.FormOption{Value: option.Value, Label: option.Label})
		}
		view.Props.Fields = append(view.Props.Fields, item)
		view.Defaults[field.Name] = field.Default
	}
	return view, nil
}
