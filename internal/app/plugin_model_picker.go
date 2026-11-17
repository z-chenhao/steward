package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readModelPicker(ctx context.Context, reference string) (contract.PickerPresentation, error) {
	return a.readModelPickerWithHost(ctx, a.Plugins(), reference)
}
func (a *App) readModelPickerWithHost(ctx context.Context, host rsi.Host, reference string) (contract.PickerPresentation, error) {
	if reference == "" {
		return contract.PickerPresentation{}, rsi.ErrNotFound
	}
	binding, err := host.ServiceInstance(ctx, reference, contract.Menu)
	if err != nil {
		return contract.PickerPresentation{}, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return contract.PickerPresentation{}, err
	}
	present := false
	for _, service := range pkg.Manifest.Provides {
		if service.Contract == contract.Picker {
			present = true
		}
	}
	if !present {
		// Only the actual pre-picker family may use its named frozen interpreter.
		// A current program failure or deactivation never reaches this branch.
		if pkg.Manifest.PluginID == contract.PluginID && pkg.Manifest.Version == "1.0.0" {
			if err := host.Store.CheckGraph(ctx, reference); err != nil {
				return contract.PickerPresentation{}, err
			}
			return legacyv1.Picker(), nil
		}
		return contract.PickerPresentation{}, rsi.ErrNotFound
	}
	raw, err := host.ReadService(ctx, reference, contract.Picker, json.RawMessage(`{}`), nil)
	if err != nil {
		return contract.PickerPresentation{}, err
	}
	var out contract.PickerPresentation
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return contract.PickerPresentation{}, err
	}
	return out, nil
}
