package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readWebWorkbench(ctx context.Context, reference string) (contract.Presentation, string, bool, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.Presentation{}, "", true, err
		}
	}
	return a.readWebWorkbenchWithHost(ctx, a.Plugins(), reference)
}
func (a *App) readWebWorkbenchWithHost(ctx context.Context, h rsi.Host, reference string) (contract.Presentation, string, bool, error) {
	revision, managed, err := a.store.WebWorkbenchSelection(ctx)
	if err != nil || !managed {
		if err == nil && reference != "" {
			err = rsi.ErrNotFound
		}
		return contract.Presentation{}, "", managed, err
	}
	if reference != "" {
		revision = reference
	}
	if revision == "" {
		return contract.Presentation{}, "", true, rsi.ErrNotFound
	}
	raw, err := h.ReadService(ctx, revision, contract.Service, json.RawMessage(`{}`), nil)
	if err != nil {
		return contract.Presentation{}, "", true, err
	}
	var out contract.Presentation
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return contract.Presentation{}, "", true, err
	}
	return out, revision, true, nil
}
