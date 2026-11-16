package referencepricing

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	catalog := New()
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: func(_ context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(call.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		raw, err := json.Marshal(contract.Response{Table: catalog.Lookup(req)})
		return rsiplugin.Result{Output: raw}, err
	}}}
}
