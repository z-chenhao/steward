package appsaccounts

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func sessionFlow(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.SessionRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected connection inputs")
	}
	return rsiplugin.JSONResult(ctx, chooseSessionFlow(request))
}
func chooseSessionFlow(request contract.SessionRequest) contract.SessionPlan {
	switch request.Step {
	case "begin":
		return contract.SessionPlan{Kind: "toolkit"}
	case "toolkit":
		if request.NoAuth {
			return contract.SessionPlan{Kind: "public"}
		}
		return contract.SessionPlan{Kind: "auth"}
	case "auth":
		return contract.SessionPlan{Kind: "session"}
	case "session":
		return contract.SessionPlan{Kind: "link"}
	case "link":
		return contract.SessionPlan{Kind: "pending", TTLSeconds: 600}
	default:
		return contract.SessionPlan{Kind: "reject"}
	}
}
