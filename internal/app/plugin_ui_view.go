package app

import (
	"context"
	"io"
	"os"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// WebWidgetView uses the delivered widget's immutable Run graph, never the
// current selection. Native interpretation is restricted to its old Run service
// boundary; a missing managed definition or resource fails closed.
func (a *App) WebWidgetView(ctx context.Context, ref string) (ui.WidgetView, error) {
	source, err := a.store.WebWidgetSource(ctx, ref)
	view := ui.WidgetView{Document: source.Document, Managed: source.Managed}
	if err != nil || !source.Managed {
		return view, err
	}
	if source.GraphRevision == "" {
		return view, rsi.ErrInvalid
	}
	host := a.Plugins()
	normalized, err := host.ServiceInstance(ctx, source.GraphRevision, contract.Normalize)
	if err != nil {
		return view, err
	}
	defined, err := host.ServiceInstance(ctx, source.GraphRevision, contract.DefinitionContract)
	if err != nil {
		return view, err
	}
	if normalized.InstanceID != defined.InstanceID || normalized.PackageDigest != defined.PackageDigest {
		return view, rsi.ErrInvalid
	}
	raw, err := a.store.WebWidgetDefinition(ctx, ref, defined.InstanceID)
	if err != nil {
		return view, err
	}
	var definition contract.Definition
	if err = rsiplugin.DecodeResult(raw, &definition); err != nil {
		return view, err
	}
	pkg, err := host.Catalog.Load(defined.PackageDigest)
	if err != nil {
		return view, err
	}
	view.Runtime, err = readUIAsset(host.Catalog, pkg, definition.Runtime, contract.RuntimeResource)
	if err != nil {
		return view, err
	}
	view.Styles, err = readUIAsset(host.Catalog, pkg, definition.Styles, contract.StylesResource)
	if err != nil {
		return view, err
	}
	// Recheck publication/owner and Guardian after resource I/O.
	current, err := a.store.WebWidgetSource(ctx, ref)
	if err != nil {
		return view, err
	}
	if !current.Managed || current.GraphRevision != source.GraphRevision || ui.Hash(current.Document) != ui.Hash(source.Document) {
		return view, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, source.GraphRevision); err != nil {
		return view, err
	}
	return view, nil
}

func readUIAsset(catalog rsi.Catalog, pkg rsi.Package, resource contract.ViewResource, name string) ([]byte, error) {
	if resource.Name != name {
		return nil, rsi.ErrInvalid
	}
	path := ""
	count := 0
	for _, r := range pkg.Manifest.Resources {
		if r.Kind == "view" && r.Name == name {
			path = r.Path
			count++
		}
	}
	if count != 1 {
		return nil, rsi.ErrInvalid
	}
	digest := ""
	for _, f := range pkg.Manifest.Files {
		if f.Path == path {
			digest = f.Digest
		}
	}
	if digest == "" || digest != resource.Digest {
		return nil, rsi.ErrInvalid
	}
	root, err := os.OpenRoot(catalog.Path(pkg.Digest))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, rsi.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 32<<20 || rsiplugin.FileDigest(raw) != digest {
		return nil, rsi.ErrInvalid
	}
	return raw, nil
}

// UIPreviewStyles resolves the private session's exact Run package, including
// previews which precede any published widget/definition receipt. Reading CSS
// never executes a plugin or borrows the current selection's resources.
func (a *App) UIPreviewStyles(ctx context.Context, runID string) ([]byte, bool, error) {
	revision, service, err := a.store.UIPreviewBinding(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	if service == "" {
		return nil, false, nil
	}
	host := a.Plugins()
	binding, err := host.ServiceInstance(ctx, revision, service)
	if err != nil {
		return nil, true, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return nil, true, err
	}
	resource := declaredUIStyles(pkg)
	raw, err := readUIAsset(host.Catalog, pkg, resource, contract.StylesResource)
	if err != nil {
		return nil, true, err
	}
	current, currentService, err := a.store.UIPreviewBinding(ctx, runID)
	if err != nil {
		return nil, true, err
	}
	if current != revision || currentService != service {
		return nil, true, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, revision); err != nil {
		return nil, true, err
	}
	return raw, true, nil
}
