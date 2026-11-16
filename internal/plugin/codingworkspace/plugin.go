package codingworkspace

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.ImportContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.ImportFacts
			if err := rsiplugin.Decode(c.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, facts.Ceilings)
		},
		contract.ReportContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.ReportFacts
			if err := rsiplugin.Decode(c.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, contract.ReportRecipe{Commands: facts.Ceilings.Commands, CommandBytes: facts.Ceilings.CommandBytes, PatchBytes: facts.Ceilings.PatchBytes, Order: "oldest"})
		},
	}}
}
