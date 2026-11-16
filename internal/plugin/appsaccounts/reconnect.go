package appsaccounts

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func reconnect(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.ReconnectRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected reconnect inputs")
	}
	return rsiplugin.JSONResult(ctx, chooseReconnect(request))
}
func chooseReconnect(request contract.ReconnectRequest) contract.ReconnectPlan {
	switch request.Step {
	case "begin":
		return contract.ReconnectPlan{Kind: "close"}
	case "closed":
		return contract.ReconnectPlan{Kind: "connect"}
	default:
		return contract.ReconnectPlan{Kind: "deny"}
	}
}
