package taskcontrol

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.LabelContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.LabelFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			r := []rune(strings.Join(strings.Fields(f.Text), " "))
			if len(r) > 80 {
				r = r[:80]
			}
			return rsiplugin.JSONResult(ctx, contract.LabelPlan{Title: string(r)})
		},
		contract.SearchContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var q contract.Request
			if err := rsiplugin.Decode(c.Input, &q); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(c.Inputs) != 1 || c.Inputs[0].ID != q.InputID {
				return rsiplugin.Result{}, errors.New("missing authorized task metadata")
			}
			raw, err := rsiplugin.ReadInput(ctx, c.Inputs[0])
			if err != nil {
				return rsiplugin.Result{}, err
			}
			var f contract.SearchFacts
			if err = rsiplugin.DecodeInput(raw, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Limit < 1 || f.Limit > 50 || len(f.Query) > 4096 {
				return rsiplugin.Result{}, errors.New("invalid result budget")
			}
			return rsiplugin.JSONResult(ctx, Rank(f))
		},
	}}
}
