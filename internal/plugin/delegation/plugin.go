package delegation

import (
	"context"
	_ "embed"
	"github.com/z-chenhao/assistant/internal/plugin/delegation/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
)

//go:embed instructions.md
var instructions string

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.Ceilings
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, contract.Policy{Depth: f.Depth, Descendants: f.Descendants, Instructions: strings.TrimSuffix(instructions, "\n")})
		}}}
}
