package experiencelearning

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var q contract.Request
		if err := rsiplugin.Decode(c.Input, &q); err != nil {
			return rsiplugin.Result{}, err
		}
		n := 1
		switch q.Reason {
		case "success":
			n = 2
		case "correction", "recovered_failure":
		default:
			return rsiplugin.Result{}, errors.New("invalid learning reason")
		}
		return rsiplugin.JSONResult(ctx, contract.Policy{RequiredRuns: n, Introduction: ""})
	}}}
}
