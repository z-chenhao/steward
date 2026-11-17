package app

import (
	"context"
	"reflect"
	"slices"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// WebTableView reads accepted output and original resources. No candidate runs,
// and active selection never reinterprets a published historical component.
func (a *App) WebTableView(ctx context.Context, ref string) (ui.TableView, error) {
	accepted, graph, err := a.store.WebTableViewSource(ctx, ref)
	if err != nil {
		return ui.TableView{}, err
	}
	host := a.Plugins()
	binding, err := host.ServiceInstance(ctx, graph, contract.TableViews)
	if err != nil {
		return ui.TableView{}, err
	}
	for _, service := range []string{contract.Normalize, contract.DefinitionContract} {
		original, err := host.ServiceInstance(ctx, graph, service)
		if err != nil {
			return ui.TableView{}, err
		}
		if original.InstanceID != binding.InstanceID || original.PackageDigest != binding.PackageDigest {
			return ui.TableView{}, rsi.ErrInvalid
		}
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return ui.TableView{}, err
	}
	styles, err := readUIAsset(host.Catalog, pkg, declaredUIStyles(pkg), contract.StylesResource)
	if err != nil {
		return ui.TableView{}, err
	}
	current, currentGraph, err := a.store.WebTableViewSource(ctx, ref)
	if err != nil {
		return ui.TableView{}, err
	}
	sameContexts := slices.EqualFunc(accepted.Contexts, current.Contexts, func(x, y contract.TableSortContext) bool {
		sameLabel := x.Description.Sort == nil && y.Description.Sort == nil || x.Description.Sort != nil && y.Description.Sort != nil && *x.Description.Sort == *y.Description.Sort
		return x.Sort == y.Sort && x.Direction == y.Direction && x.Description.Title == y.Description.Title && x.Description.Direction == y.Description.Direction && sameLabel
	})
	if graph != currentGraph || accepted.Component != current.Component || accepted.HTML != current.HTML || accepted.CSS != current.CSS || accepted.Worker != current.Worker || accepted.CompactMaxWidth != current.CompactMaxWidth || !sameContexts {
		return ui.TableView{}, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, graph); err != nil {
		return ui.TableView{}, err
	}
	contexts := make([]ui.TableSortContext, len(accepted.Contexts))
	for i, c := range accepted.Contexts {
		contexts[i] = ui.TableSortContext{Sort: c.Sort, Direction: c.Direction, Description: ui.TableDescription{Title: c.Description.Title, Sort: c.Description.Sort, Direction: c.Description.Direction}}
	}
	return ui.TableView{HTML: accepted.HTML, CSS: accepted.CSS, Worker: accepted.Worker, Styles: styles, CompactMaxWidth: accepted.CompactMaxWidth, Contexts: contexts}, nil
}

// WebTableExport rechecks original facts after package/resource authorization.
// Selection changes cannot redirect a historical download to another version.
func (a *App) WebTableExport(ctx context.Context, ref string, rows []int) (ui.TableExport, error) {
	original, graph, err := a.store.WebTableExportSource(ctx, ref, rows)
	if err != nil {
		return ui.TableExport{}, err
	}
	if _, err = a.WebTableView(ctx, ref); err != nil {
		return ui.TableExport{}, err
	}
	current, currentGraph, err := a.store.WebTableExportSource(ctx, ref, rows)
	if err != nil {
		return ui.TableExport{}, err
	}
	if graph != currentGraph || !reflect.DeepEqual(original, current) {
		return ui.TableExport{}, rsi.ErrConflict
	}
	if err = a.Plugins().Store.CheckGraph(ctx, graph); err != nil {
		return ui.TableExport{}, err
	}
	return original, nil
}
