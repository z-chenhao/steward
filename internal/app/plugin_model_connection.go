package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readModelConnection(ctx context.Context, facts contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
	// Setup is available before a provider exists. Prepare its actual owner before
	// accepting a candidate or first-login choice, rather than charging the first
	// full bundle verification to the later runtime-admission deadline.
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.ConnectionDefaults{}, true, err
		}
	}
	return a.readModelConnectionWithHost(ctx, a.Plugins(), facts)
}
func (a *App) readModelConnectionWithHost(ctx context.Context, host rsi.Host, facts contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
	revision, managed, err := a.store.ModelConnectionSelection(ctx)
	if err != nil || !managed {
		return contract.ConnectionDefaults{}, managed, err
	}
	if revision == "" {
		return contract.ConnectionDefaults{}, true, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.ConnectionDefaults{}, true, err
	}
	raw, err := host.ReadService(ctx, revision, contract.ConnectionPolicy, input, nil)
	if err != nil {
		return contract.ConnectionDefaults{}, true, err
	}
	var out contract.ConnectionDefaults
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return contract.ConnectionDefaults{}, true, err
	}
	return out, true, nil
}
