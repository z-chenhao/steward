package app

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/operations/service"
	"github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ServiceRestartRecipe is a startup composition port. No root paths, settings,
// health, counters, process identities, credentials or restart authority cross
// the producer boundary. The service session freezes this accepted recipe.
func (a *App) ServiceRestartRecipe(ctx context.Context) (*service.RestartReceipt, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return nil, err
		}
	}
	return a.serviceRestartRecipeWithHost(ctx, a.Plugins())
}
func (a *App) serviceRestartRecipeWithHost(ctx context.Context, h rsi.Host) (*service.RestartReceipt, error) {
	revision, managed, err := a.store.ServiceRestartSelection(ctx)
	if err != nil {
		return nil, err
	}
	if !managed || revision == "" {
		return nil, rsi.ErrNotFound
	}
	raw, err := h.ReadService(ctx, revision, contract.Service, json.RawMessage(`{}`), nil)
	if err != nil {
		return nil, err
	}
	var recipe contract.Recipe
	if err = rsiplugin.DecodeResult(raw, &recipe); err != nil || recipe.RetryDelayMS == 0 || recipe.AlertAfterSeconds > 3600 {
		return nil, rsi.ErrInvalid
	}
	return &service.RestartReceipt{GraphRevision: revision, Recipe: service.RestartRecipe{AlertAfterSeconds: recipe.AlertAfterSeconds, Attempts: recipe.Attempts, RetryDelayMS: recipe.RetryDelayMS}}, nil
}
