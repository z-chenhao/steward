// Package evidencereader owns independently versioned evidence pagination.
package evidencereader

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/evidencereader/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.TextContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.TextFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Bytes < 0 || f.Bytes > 4096 || len(f.Boundaries) > 4097 || len(f.Breaks) > 4096 {
				return rsiplugin.Result{}, errors.New("invalid text boundaries")
			}
			end := 0
			for _, n := range f.Boundaries {
				if n > end && n <= f.Bytes {
					end = n
				}
			}
			return rsiplugin.JSONResult(ctx, contract.TextPlan{Bytes: end})
		},
		contract.LinksContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.LinkFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Candidates < 0 || f.Candidates > 21 {
				return rsiplugin.Result{}, errors.New("invalid link count")
			}
			return rsiplugin.JSONResult(ctx, contract.LinkPlan{Take: min(20, f.Candidates)})
		}}}
}
