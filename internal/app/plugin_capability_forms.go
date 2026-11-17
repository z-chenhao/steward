package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/legacyv1"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"unicode/utf8"
)

func (a *App) readCapabilityForm(ctx context.Context, in contract.Request) (contract.Result, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.Result{}, err
		}
	}
	if a.store == nil {
		return legacyv1.Project(in), nil
	}
	return a.readCapabilityFormWithHost(ctx, a.Plugins(), in)
}
func (a *App) readCapabilityFormWithHost(ctx context.Context, host rsi.Host, in contract.Request) (contract.Result, error) {
	revision, managed, err := a.store.CapabilityFormSelection(ctx)
	if err != nil {
		return contract.Result{}, err
	}
	if !managed {
		return legacyv1.Project(in), nil
	}
	if revision == "" {
		return contract.Result{}, rsi.ErrNotFound
	}
	input, err := json.Marshal(in)
	if err != nil {
		return contract.Result{}, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Service, input, nil)
	if err != nil {
		return contract.Result{}, err
	}
	var out contract.Result
	err = rsiplugin.DecodeResult(raw, &out)
	if err != nil {
		return out, err
	}
	if in.WithView {
		binding, e := host.ServiceInstance(ctx, revision, contract.Service)
		if e != nil {
			return contract.Result{}, e
		}
		pkg, e := host.Catalog.Load(binding.PackageDigest)
		if e != nil {
			return contract.Result{}, e
		}
		for _, asset := range []struct {
			name, file string
			value      *contract.Asset
		}{{contract.ViewResource, contract.ViewFile, out.View}, {contract.CopyResource, contract.CopyFile, out.Copy}} {
			count, digest := 0, ""
			for _, resource := range pkg.Manifest.Resources {
				if resource.Name == asset.name && resource.Kind == "view" && resource.Path == asset.file {
					count++
					for _, file := range pkg.Manifest.Files {
						if file.Path == resource.Path {
							digest = file.Digest
						}
					}
				}
			}
			v := asset.value
			if count != 1 || digest == "" || v == nil || v.Resource != asset.name || v.Digest != digest || len(v.Source) == 0 || len(v.Source) > 65536 || !utf8.ValidString(v.Source) || rsiplugin.FileDigest([]byte(v.Source)) != digest {
				return contract.Result{}, rsi.ErrInvalid
			}
		}
		if e := host.Store.CheckGraph(ctx, revision); e != nil {
			return contract.Result{}, e
		}
	} else if out.View != nil || out.Copy != nil {
		return contract.Result{}, rsi.ErrInvalid
	}
	return out, nil
}

func (a *App) readCapabilityPresentation(ctx context.Context, facts contract.PresentationFacts) (contract.Presentation, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.Presentation{}, err
		}
	}
	if a.store == nil {
		return legacyv1.ProjectPresentation(facts), nil
	}
	return a.readCapabilityPresentationWithHost(ctx, a.Plugins(), facts)
}
func (a *App) readCapabilityPresentationWithHost(ctx context.Context, host rsi.Host, facts contract.PresentationFacts) (contract.Presentation, error) {
	revision, managed, err := a.store.CapabilityPresentationSelection(ctx)
	if err != nil {
		return contract.Presentation{}, err
	}
	if !managed {
		return legacyv1.ProjectPresentation(facts), nil
	}
	if revision == "" {
		return contract.Presentation{}, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.Presentation{}, err
	}
	raw, err := host.ReadService(ctx, revision, contract.PresentationService, input, nil)
	if err != nil {
		return contract.Presentation{}, err
	}
	var out contract.Presentation
	err = rsiplugin.DecodeResult(raw, &out)
	return out, err
}
