package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// A terminal session freezes its display/choice owner once. Only bounded facts
// reach the service. Original terminal input, auth URLs and rendered messages
// are substituted locally, never supplied to the plugin or its call ledger.
func (a *App) terminalInterfaceReader(host func() rsi.Host) func(context.Context, contract.Query) (contract.Result, bool, error) {
	var initialized, managed bool
	var revision string
	return func(ctx context.Context, q contract.Query) (contract.Result, bool, error) {
		if !initialized {
			if a.prepareFeaturePlugins != nil {
				if err := a.prepareFeaturePlugins(ctx); err != nil {
					return contract.Result{}, true, err
				}
			}
			if a.store == nil {
				initialized = true
				return contract.Result{}, false, nil
			}
			var err error
			revision, managed, err = a.store.TerminalInterfaceSelection(ctx)
			if err != nil {
				return contract.Result{}, managed, err
			}
			initialized = true
		}
		if !managed {
			return contract.Result{}, false, nil
		}
		if revision == "" {
			return contract.Result{}, true, rsi.ErrRevoked
		}
		data, err := json.Marshal(q)
		if err != nil {
			return contract.Result{}, true, err
		}
		raw, err := host().ReadService(ctx, revision, contract.Service, data, nil)
		if err != nil {
			return contract.Result{}, true, err
		}
		var out contract.Result
		if err = rsiplugin.DecodeResult(raw, &out); err != nil {
			return contract.Result{}, true, err
		}
		return out, true, nil
	}
}
