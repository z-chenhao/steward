package appsaccounts

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/v1"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func accountCheck(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.AccountCheckRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected account check input")
	}
	return rsiplugin.JSONResult(ctx, v1.Check(request))
}
