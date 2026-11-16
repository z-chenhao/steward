package appslogos

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appslogos/artwork"
	"github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"time"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.apps-logos", Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Logo: logo}}
}
func logo(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input contract.Request
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, choose(input))
}
func choose(input contract.Request) contract.Plan {
	const seconds = 86400
	if input.Cached && input.AgeNS < int64(24*time.Hour) {
		return contract.Plan{Kind: "cached", CacheSeconds: seconds}
	}
	if body, err := artwork.Read(input.Slug); err == nil {
		return contract.Plan{Kind: "bundled", Body: body, CacheSeconds: seconds}
	}
	if input.Registered {
		return contract.Plan{Kind: "provider", CacheSeconds: seconds}
	}
	return contract.Plan{Kind: "unavailable", CacheSeconds: seconds}
}
