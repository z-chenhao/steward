package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readModelMenu(ctx context.Context, facts contract.Facts) (contract.Projection, bool, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.Projection{}, true, err
		}
	}
	return a.readModelMenuWithHost(ctx, a.Plugins(), facts)
}

func (a *App) readModelMenuWithHost(ctx context.Context, host rsi.Host, facts contract.Facts) (contract.Projection, bool, error) {
	revision, managed, err := a.store.ModelMenuSelection(ctx)
	if err != nil || !managed {
		if err == nil && facts.ReaderRevision != "" {
			err = rsi.ErrNotFound
		}
		return contract.Projection{}, managed, err
	}
	if facts.ReaderRevision != "" {
		revision = facts.ReaderRevision
	}
	if revision == "" {
		return contract.Projection{}, true, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.Projection{}, true, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Menu, input, nil)
	if err != nil {
		return contract.Projection{}, true, err
	}
	var out contract.Projection
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return contract.Projection{}, true, err
	}
	out.ReaderRevision = revision
	return out, true, nil
}
