package capabilityforms

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/v1"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.PresentationService: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var facts contract.PresentationFacts
		if err := rsiplugin.Decode(call.Input, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, v1.ProjectPresentation(facts))
	}, contract.Service: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var in contract.Request
		if err := rsiplugin.Decode(call.Input, &in); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, v1.Project(in))
	}}}
}
