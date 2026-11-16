package appsinterface

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only the fixed owning file is readable; no actual row, caller path, State or effects.
func compileSidebarView(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	raw, source, err := readOwnViewSource(contract.SidebarViewFile)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.SidebarView{Resource: contract.SidebarViewResource, Digest: rsiplugin.FileDigest(raw), Program: source.Program})
}
