package shellpolicy

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/shellpolicy/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var f contract.Facts
		if err := rsiplugin.Decode(c.Input, &f); err != nil {
			return rsiplugin.Result{}, err
		}
		if f.Ceilings.TimeoutSeconds < 1 || f.Ceilings.OutputBytes < 1 {
			return rsiplugin.Result{}, errors.New("invalid shell ceilings")
		}
		timeout := min(60, f.Ceilings.TimeoutSeconds)
		if f.RequestedTimeout != nil {
			if *f.RequestedTimeout < 1 {
				return rsiplugin.Result{}, errors.New("invalid requested deadline")
			}
			timeout = min(*f.RequestedTimeout, f.Ceilings.TimeoutSeconds)
		}
		return rsiplugin.JSONResult(ctx, contract.Limits{TimeoutSeconds: timeout, OutputBytes: f.Ceilings.OutputBytes})
	}}}
}
