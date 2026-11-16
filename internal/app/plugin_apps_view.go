package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type appsViewContribution struct {
	Output   json.RawMessage
	Digest   string
	Provided bool
}

func readAppsViewContribution(ctx context.Context, h rsi.Host, revision, service, name string) (appsViewContribution, error) {
	var out appsViewContribution
	graph, err := h.Store.Graph(ctx, revision)
	if err != nil {
		return out, err
	}
	var selected rsiplugin.Binding
	for _, b := range graph.Graph.Bindings {
		pkg, err := h.Catalog.Load(b.PackageDigest)
		if err != nil {
			return out, err
		}
		for _, s := range pkg.Manifest.Provides {
			if s.Contract == service {
				if selected.InstanceID != "" {
					return out, rsi.ErrConflict
				}
				selected = b
			}
		}
	}
	if selected.InstanceID == "" {
		return out, nil
	}
	out.Provided = true
	pkg, err := h.Catalog.Load(selected.PackageDigest)
	if err != nil {
		return out, err
	}
	path := ""
	count := 0
	for _, r := range pkg.Manifest.Resources {
		if r.Name == name && r.Kind == "view" {
			path = r.Path
			count++
		}
	}
	if count != 1 {
		return out, rsi.ErrInvalid
	}
	for _, file := range pkg.Manifest.Files {
		if file.Path == path {
			out.Digest = file.Digest
		}
	}
	if out.Digest == "" {
		return out, rsi.ErrInvalid
	}
	out.Output, err = h.ReadService(ctx, revision, service, json.RawMessage(`{}`), nil)
	return out, err
}
