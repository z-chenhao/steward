package codemodepolicy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/codemodepolicy/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.LimitsContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.LimitsFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Role != "main" && f.Role != "worker" {
				return rsiplugin.Result{}, errors.New("unsupported code role")
			}
			return rsiplugin.JSONResult(ctx, contract.LimitsPlan{Limits: f.Ceilings})
		},
		contract.FormatContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var q contract.Request
			if err := rsiplugin.Decode(c.Input, &q); err != nil {
				return rsiplugin.Result{}, err
			}
			if len(c.Inputs) != 1 || c.Inputs[0].ID != q.InputID {
				return rsiplugin.Result{}, errors.New("missing frozen input schema")
			}
			raw, err := rsiplugin.ReadInput(ctx, c.Inputs[0])
			if err != nil {
				return rsiplugin.Result{}, err
			}
			var schema any
			if err = json.Unmarshal(raw, &schema); err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, contract.FormatPlan{InputType: inputType(schema), Guidance: ""})
		}}}
}
