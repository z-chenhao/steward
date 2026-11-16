// Package memorycurator owns independently versioned extraction and Dreaming
// methods. Host validates evidence, plans, ownership, costs and all mutations.
package memorycurator

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/memorycurator/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed facts.md
var facts string

//go:embed dream.md
var dream string

//go:embed grounded.md
var grounded string

//go:embed worker.md
var worker string

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.ScheduleContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var req struct{}
			if err := rsiplugin.Decode(c.Input, &req); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(c.Inputs) != 0 {
				return rsiplugin.Result{}, errors.New("schedule methods do not read source data")
			}
			return rsiplugin.JSONResult(ctx, contract.Schedule{MinChanges: 8, MaxWaitSeconds: 300})
		},
		contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var req contract.Request
			if err := rsiplugin.Decode(c.Input, &req); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(c.Inputs) != 0 {
				return rsiplugin.Result{}, errors.New("maintenance methods do not read source data")
			}
			text, ok := map[string]string{"facts": facts, "dream": dream, "grounded": grounded, "worker": worker}[req.Style]
			if !ok {
				return rsiplugin.Result{}, errors.New("unsupported maintenance method")
			}
			return rsiplugin.JSONResult(ctx, contract.Policy{Instructions: strings.TrimSuffix(text, "\n")})
		}}}
}
