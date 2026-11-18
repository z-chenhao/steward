package memoryinspector

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.1.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Presentation: present}}
}
func present(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var req contract.Request
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected private inspection input")
	}
	if err := rsiplugin.Decode(call.Input, &req); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(req.Navigation) > 4096 {
		return rsiplugin.Result{}, errors.New("too many navigation targets")
	}
	return rsiplugin.JSONResult(ctx, project(req))
}
