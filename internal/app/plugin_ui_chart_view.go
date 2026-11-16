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
func (a *App) WebChartView(ctx context.Context, ref string) (ui.ChartView, error) {
	accepted, facts, graph, err := a.store.WebChartViewSource(ctx, ref)
	if err != nil {
		return ui.ChartView{}, err
	}
	host := a.Plugins()
	binding, err := host.ServiceInstance(ctx, graph, contract.ChartViews)
	if err != nil {
		return ui.ChartView{}, err
	}
	for _, service := range []string{contract.Normalize, contract.DefinitionContract} {
		original, e := host.ServiceInstance(ctx, graph, service)
		if e != nil {
			return ui.ChartView{}, e
		}
		if original.InstanceID != binding.InstanceID || original.PackageDigest != binding.PackageDigest {
			return ui.ChartView{}, rsi.ErrInvalid
		}
	}
	if _, err = host.Catalog.Load(binding.PackageDigest); err != nil {
		return ui.ChartView{}, err
	}
	current, currentFacts, currentGraph, err := a.store.WebChartViewSource(ctx, ref)
	if err != nil {
		return ui.ChartView{}, err
	}
	if currentGraph != graph || current.Component != accepted.Component || !reflect.DeepEqual(current, accepted) || currentFacts.Name != facts.Name || !bytes.Equal(currentFacts.Props, facts.Props) {
		return ui.ChartView{}, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, graph); err != nil {
		return ui.ChartView{}, err
	}
	return ui.ChartView{Title: accepted.Title, Description: accepted.Description, Unit: accepted.Unit, Worker: accepted.Worker, Name: facts.Name, Props: facts.Props}, nil
}

// Recheck accepted policy and original facts after package I/O, before release.
func (a *App) WebChartExport(ctx context.Context, ref string, rows []int) (ui.ChartExport, error) {
	original, graph, err := a.store.WebChartExportSource(ctx, ref, rows)
	if err != nil {
		return ui.ChartExport{}, err
	}
	if _, err = a.WebChartView(ctx, ref); err != nil {
		return ui.ChartExport{}, err
	}
	current, currentGraph, err := a.store.WebChartExportSource(ctx, ref, rows)
	if err != nil {
		return ui.ChartExport{}, err
	}
	if graph != currentGraph || !reflect.DeepEqual(original, current) {
		return ui.ChartExport{}, rsi.ErrConflict
	}
	if err = a.Plugins().Store.CheckGraph(ctx, graph); err != nil {
		return ui.ChartExport{}, err
	}
	return original, nil
}
