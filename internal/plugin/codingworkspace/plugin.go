package codingworkspace

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.4", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.DraftContract:   draftRecipe,
		contract.ProjectContract: projectPlan,
		contract.CheckContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var facts contract.CheckFacts
			if err := rsiplugin.Decode(c.Input, &facts); err != nil {
				return rsiplugin.Result{}, err
			}
			timeout := min(60, facts.Ceilings.TimeoutSeconds)
			if facts.RequestedTimeout != nil {
				timeout = min(*facts.RequestedTimeout, facts.Ceilings.TimeoutSeconds)
			}
			return rsiplugin.JSONResult(ctx, contract.CheckLimits{TimeoutSeconds: timeout, OutputBytes: facts.Ceilings.OutputBytes})
		},
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
			return rsiplugin.JSONResult(ctx, contract.ReportRecipe{Commands: facts.Ceilings.Commands, CommandBytes: facts.Ceilings.CommandBytes, PatchBytes: facts.Ceilings.PatchBytes, Order: "oldest", ReviewGuide: []string{"Compare the original instructions and later changes with the candidate diff; record unmet requirements.", "Check candidate, environment and method identities for each receipt; a historical pass may not cover this candidate.", "Inspect retained evidence, failures, unknown effects and unverified behavior before accepting the result.", "Review reported time, usage and prices with missing observations left unavailable.", "Confirm the exact remote revision and content before delivery; record the human acceptance decision separately."}})
		},
	}}
}
