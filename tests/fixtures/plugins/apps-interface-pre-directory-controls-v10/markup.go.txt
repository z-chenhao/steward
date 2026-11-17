package appsinterface

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func compileAccountMarkup(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	raw, err := readOwnViewBytesLimit(contract.MarkupFile, 16384)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.Markup{Resource: contract.MarkupResource, Digest: rsiplugin.FileDigest(raw), Source: string(raw)})
}
