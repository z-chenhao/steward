package appsaccounts

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func accountClose(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.AccountCloseRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected cleanup inputs")
	}
	return rsiplugin.JSONResult(ctx, chooseAccountClose(request))
}
func chooseAccountClose(request contract.AccountCloseRequest) contract.AccountClosePlan {
	switch request.Step {
	case "closed":
		return contract.AccountClosePlan{Kind: "account"}
	case "account":
		if request.Failed || !request.Matched {
			return contract.AccountClosePlan{Kind: "done"}
		}
		return contract.AccountClosePlan{Kind: "revoke"}
	case "revoked":
		return contract.AccountClosePlan{Kind: "delete"}
	default:
		return contract.AccountClosePlan{Kind: "done"}
	}
}
