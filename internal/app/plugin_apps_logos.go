package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readAppsLogos(ctx context.Context, revision string) (web.AppLogoPolicy, error) {
	return a.appsLogosWithHost(ctx, a.Plugins(), revision)
}
func (a *App) appsLogosWithHost(ctx context.Context, host rsi.Host, reference string) (web.AppLogoPolicy, error) {
	revision, managed, err := a.store.AppsLogoSelection(ctx)
	if err != nil {
		return web.AppLogoPolicy{}, err
	}
	if reference != "" {
		revision = reference
		managed = true
	}
	if !managed {
		return web.NativeAppLogos(), nil
	}
	if err := host.Store.CheckGraph(ctx, revision); err != nil {
		return web.AppLogoPolicy{}, err
	}
	if _, err := host.ServiceInstance(ctx, revision, contract.Logo); err != nil {
		return web.AppLogoPolicy{}, err
	}
	return web.ManagedAppLogos(revision, func(c context.Context, request contract.Request) (contract.Plan, error) {
		input, err := json.Marshal(request)
		if err != nil {
			return contract.Plan{}, err
		}
		raw, err := host.ReadService(c, revision, contract.Logo, input, nil)
		if err != nil {
			return contract.Plan{}, err
		}
		var plan contract.Plan
		if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.Plan{}, err
		}
		return plan, nil
	}, func(c context.Context) error { return host.Store.CheckGraph(c, revision) }), nil
}
