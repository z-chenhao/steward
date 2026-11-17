package app

import (
	"context"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// WebPlanView is a read of an already accepted view under its original graph.
// It never runs a candidate or consults the current owner selection.
func (a *App) WebPlanView(ctx context.Context, ref string) (ui.PlanView, error) {
	accepted, graph, err := a.store.WebPlanViewSource(ctx, ref)
	if err != nil {
		return ui.PlanView{}, err
	}
	host := a.Plugins()
	viewBinding, err := host.ServiceInstance(ctx, graph, contract.PlanViews)
	if err != nil {
		return ui.PlanView{}, err
	}
	for _, service := range []string{contract.Normalize, contract.DefinitionContract} {
		binding, err := host.ServiceInstance(ctx, graph, service)
		if err != nil {
			return ui.PlanView{}, err
		}
		if binding.InstanceID != viewBinding.InstanceID || binding.PackageDigest != viewBinding.PackageDigest {
			return ui.PlanView{}, rsi.ErrInvalid
		}
	}
	pkg, err := host.Catalog.Load(viewBinding.PackageDigest)
	if err != nil {
		return ui.PlanView{}, err
	}
	styles, err := readUIAsset(host.Catalog, pkg, declaredUIStyles(pkg), contract.StylesResource)
	if err != nil {
		return ui.PlanView{}, err
	}
	current, currentGraph, err := a.store.WebPlanViewSource(ctx, ref)
	if err != nil {
		return ui.PlanView{}, err
	}
	if graph != currentGraph || accepted != current {
		return ui.PlanView{}, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, graph); err != nil {
		return ui.PlanView{}, err
	}
	return ui.PlanView{HTML: accepted.HTML, CSS: accepted.CSS, Styles: styles, CompactMaxWidth: accepted.CompactMaxWidth}, nil
}

func declaredUIStyles(pkg rsi.Package) contract.ViewResource {
	resource := contract.ViewResource{Name: contract.StylesResource}
	for _, role := range pkg.Manifest.Resources {
		if role.Kind == "view" && role.Name == contract.StylesResource {
			for _, file := range pkg.Manifest.Files {
				if file.Path == role.Path {
					resource.Digest = file.Digest
				}
			}
		}
	}
	return resource
}
