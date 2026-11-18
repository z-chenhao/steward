package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ReviewDemonstration uses the active managed policy on each local draft or sample check.
func (a *App) ReviewDemonstration(ctx context.Context, q contract.DemoRequest) (contract.DemoResult, error) {
	var out contract.DemoResult
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return out, err
		}
	}
	host := a.Plugins()
	status, err := host.Store.Status(ctx)
	if err != nil {
		return out, err
	}
	if status.Stopped || status.Revision == "" {
		return out, rsi.ErrNotFound
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return out, err
	}
	reply, err := host.ReadService(ctx, status.Revision, contract.Demonstration, raw, nil)
	if err != nil {
		return out, err
	}
	err = rsiplugin.DecodeResult(reply, &out)
	return out, err
}
