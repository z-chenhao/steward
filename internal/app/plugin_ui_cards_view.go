package app

import (
	"context"
	"io"
	"net/url"
	"slices"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// WebCardsView is a read of an already accepted view under its original graph.
// It never runs a candidate or consults the current owner selection.
func (a *App) WebCardsView(ctx context.Context, ref string) (ui.CardsView, error) {
	accepted, graph, err := a.store.WebCardsViewSource(ctx, ref)
	if err != nil {
		return ui.CardsView{}, err
	}
	host := a.Plugins()
	viewBinding, err := host.ServiceInstance(ctx, graph, contract.CardsViews)
	if err != nil {
		return ui.CardsView{}, err
	}
	for _, service := range []string{contract.Normalize, contract.DefinitionContract} {
		binding, err := host.ServiceInstance(ctx, graph, service)
		if err != nil {
			return ui.CardsView{}, err
		}
		if binding.InstanceID != viewBinding.InstanceID || binding.PackageDigest != viewBinding.PackageDigest {
			return ui.CardsView{}, rsi.ErrInvalid
		}
	}
	pkg, err := host.Catalog.Load(viewBinding.PackageDigest)
	if err != nil {
		return ui.CardsView{}, err
	}
	if accepted.Runtime != "" {
		switch pkg.Manifest.Version {
		case "1.13.0", "1.14.0", "1.15.0", "1.16.0":
		default:
			return ui.CardsView{}, rsi.ErrInvalid
		}
	}
	styles, err := readUIAsset(host.Catalog, pkg, declaredUIStyles(pkg), contract.StylesResource)
	if err != nil {
		return ui.CardsView{}, err
	}
	current, currentGraph, err := a.store.WebCardsViewSource(ctx, ref)
	if err != nil {
		return ui.CardsView{}, err
	}
	if graph != currentGraph || accepted.Component != current.Component || accepted.HTML != current.HTML || accepted.CSS != current.CSS || accepted.Runtime != current.Runtime || accepted.Worker != current.Worker || accepted.CompactMaxWidth != current.CompactMaxWidth || !slices.Equal(accepted.Contexts, current.Contexts) {
		return ui.CardsView{}, rsi.ErrConflict
	}
	if err = host.Store.CheckGraph(ctx, graph); err != nil {
		return ui.CardsView{}, err
	}
	contexts := make([]ui.CardsContext, len(accepted.Contexts))
	for i, context := range accepted.Contexts {
		contexts[i] = ui.CardsContext{Title: context.Title, VisibleItem: context.VisibleItem}
	}
	return ui.CardsView{HTML: accepted.HTML, CSS: accepted.CSS, Contexts: contexts, Runtime: accepted.Runtime, Worker: accepted.Worker, Styles: styles, CompactMaxWidth: accepted.CompactMaxWidth}, nil
}

// OpenWebCardsImage retains the original UI grant as well as the ordinary
// Artifact owner grant. Opening a file cannot resurrect a revoked component.
// The existing Artifact stream is retained; images are not buffered in Go.
func (a *App) OpenWebCardsImage(ctx context.Context, ref string, item int) (io.ReadCloser, string, error) {
	resource, graph, err := a.store.WebCardsImageSource(ctx, ref, item)
	if err != nil {
		return nil, "", err
	}
	if _, err = a.WebCardsView(ctx, ref); err != nil {
		return nil, "", err
	}
	descriptor, err := a.DescribeArtifact(ctx, resource)
	if err != nil {
		return nil, "", err
	}
	switch descriptor.MediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return nil, "", agent.ErrNotFound
	}
	source, err := a.OpenArtifact(ctx, resource)
	if err != nil {
		return nil, "", err
	}
	// The original file may have been deleted or expired while Open verified
	// its bytes. No stream has been handed to the caller yet: retain both
	// the Artifact version grant and the component grant at this boundary.
	currentDescriptor, err := a.DescribeArtifact(ctx, resource)
	if err == nil && (currentDescriptor.DeletedAt != nil || currentDescriptor.ExpiresAt != nil && !currentDescriptor.ExpiresAt.After(time.Now()) || currentDescriptor.Version != descriptor.Version || currentDescriptor.ContentHash != descriptor.ContentHash || currentDescriptor.MediaType != descriptor.MediaType) {
		err = rsi.ErrConflict
	}
	if err == nil {
		current, currentGraph, readErr := a.store.WebCardsImageSource(ctx, ref, item)
		err = readErr
		if err == nil && (current != resource || currentGraph != graph) {
			err = rsi.ErrConflict
		}
	}
	if err == nil {
		err = a.Plugins().Store.CheckGraph(ctx, graph)
	}
	if err != nil {
		source.Close()
		return nil, "", err
	}
	return source, descriptor.MediaType, nil
}

// WebCardsLink retains the original component and graph grants at navigation.
func (a *App) WebCardsLink(ctx context.Context, ref string, item int) (string, error) {
	destination, graph, err := a.store.WebCardsLinkSource(ctx, ref, item)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", agent.ErrNotFound
	}
	if _, err = a.WebCardsView(ctx, ref); err != nil {
		return "", err
	}
	current, currentGraph, err := a.store.WebCardsLinkSource(ctx, ref, item)
	if err != nil {
		return "", err
	}
	if current != destination || currentGraph != graph {
		return "", rsi.ErrConflict
	}
	if err = a.Plugins().Store.CheckGraph(ctx, graph); err != nil {
		return "", err
	}
	return destination, nil
}
