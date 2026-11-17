package taskfeedback

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.Facts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Kind != "input" && f.Kind != "review" {
				return rsiplugin.Result{}, errors.New("unknown feedback kind")
			}
			return rsiplugin.JSONResult(ctx, contract.Policy{LifetimeSeconds: 604800, ResponseGuidance: "If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"})
		}}}
}
