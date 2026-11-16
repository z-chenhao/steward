package taskprogress

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/taskprogress/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed guidance.md
var guidance string

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.GuidanceContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.GuidanceFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Role != "root" {
				return rsiplugin.Result{}, errors.New("root guidance only")
			}
			return rsiplugin.JSONResult(ctx, contract.Guidance{Instructions: strings.TrimSuffix(guidance, "\n")})
		},
		contract.SelectionContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.SelectionFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, contract.Selection{Publish: true})
		},
	}}
}
