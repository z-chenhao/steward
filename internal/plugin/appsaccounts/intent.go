package appsaccounts

import (
	"bytes"
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/v1"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.apps-accounts", Version: "1.8.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.ConnectIntent: connectIntent, contract.AuthConfig: authConfig, contract.SessionFlow: sessionFlow, contract.AccountCheck: accountCheck, contract.AccountClose: accountClose, contract.Reconnect: reconnect, contract.Settings: settings, contract.Inventory: inventory}}
}
func connectIntent(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.ConnectRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.ConnectInputID {
		return rsiplugin.Result{}, errors.New("invalid connection input")
	}
	var raw bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &raw); err != nil {
		return rsiplugin.Result{}, err
	}
	rows, err := contract.DecodeConnectRows(raw.Bytes(), request.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, v1.Connect(rows, request.Alias))
}
