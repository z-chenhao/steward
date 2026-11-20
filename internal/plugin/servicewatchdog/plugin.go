package servicewatchdog

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.1.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var request struct{}
		if err := rsiplugin.Decode(call.Input, &request); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, contract.Recipe{Attempts: 3, RetryDelayMS: 60000, AlertAfterSeconds: 60})
	}}}
}
