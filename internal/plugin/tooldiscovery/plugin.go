package tooldiscovery

import (
	"bytes"
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: rank}}
}
func rank(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var req contract.Request
	if err := rsiplugin.Decode(call.Input, &req); err != nil {
		return rsiplugin.Result{}, err
	}
	if req.Count < 0 || req.Limit < 1 || req.Limit > 128 || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.InputID {
		return rsiplugin.Result{}, errors.New("invalid discovery request")
	}
	var raw bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &raw); err != nil {
		return rsiplugin.Result{}, err
	}
	f, err := contract.Decode(raw.Bytes(), req.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, Rank(f, req.Limit))
}
