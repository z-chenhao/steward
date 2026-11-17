package app

import (
	"context"
	"database/sql"
	"encoding/json"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readAppsEditorial(ctx context.Context, query contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
	return a.readAppsEditorialWithHost(ctx, a.Plugins(), query)
}

func (a *App) readAppsEditorialWithHost(ctx context.Context, host rsi.Host, query contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
	revision, managed, err := a.store.AppsEditorialSelection(ctx)
	if err != nil || !managed {
		return contract.Catalogue{}, managed, nil, err
	}
	if revision == "" {
		return contract.Catalogue{}, true, nil, rsi.ErrNotFound
	}
	input, err := json.Marshal(query)
	if err != nil {
		return contract.Catalogue{}, true, nil, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Editorial, input, nil)
	if err != nil {
		return contract.Catalogue{}, true, nil, err
	}
	var value contract.Catalogue
	if err := rsiplugin.DecodeResult(raw, &value); err != nil {
		return contract.Catalogue{}, true, nil, err
	}
	projector := func(c context.Context, request contract.CategoryRequest, data []byte) (contract.CategoryPlan, error) {
		input, err := json.Marshal(request)
		if err != nil {
			return contract.CategoryPlan{}, err
		}
		raw, err := host.ReadService(c, revision, contract.CategoryOptions, input, []rsi.InputData{{ID: contract.CategoryInputID, Data: data}})
		if err != nil {
			return contract.CategoryPlan{}, err
		}
		var plan contract.CategoryPlan
		if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.CategoryPlan{}, err
		}
		return plan, nil
	}
	captionProjector := func(c context.Context, facts []contract.CaptionFacts) ([]contract.CaptionPlan, error) {
		input, err := json.Marshal(facts)
		if err != nil {
			return nil, err
		}
		raw, err := host.ReadService(c, revision, contract.Captions, input, nil)
		if err != nil {
			return nil, err
		}
		var plans []contract.CaptionPlan
		if err := rsiplugin.DecodeResult(raw, &plans); err != nil {
			return nil, err
		}
		return plans, nil
	}

	orderer := func(c context.Context, request contract.OrderRequest, data []byte) (contract.OrderPage, error) {
		input, err := json.Marshal(request)
		if err != nil {
			return contract.OrderPage{}, err
		}
		raw, err := host.ReadService(c, revision, contract.CatalogueOrder, input, []rsi.InputData{{ID: contract.OrderInputID, Data: data}})
		if err != nil {
			return contract.OrderPage{}, err
		}
		var page contract.OrderPage
		if err := rsiplugin.DecodeResult(raw, &page); err != nil {
			return contract.OrderPage{}, err
		}
		return page, nil
	}

	states := func(c context.Context, stateRevision int64, request contract.CardStateRequest, data []byte) (contract.CardStatePlan, error) {
		privateHost, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return contract.CardStatePlan{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.CardStatePlan{}, err
		}
		raw, err := privateHost.ReadService(c, revision, contract.CardStates, input, []rsi.InputData{{ID: contract.CardStateInputID, Data: data}})
		if err != nil {
			return contract.CardStatePlan{}, err
		}
		var plan contract.CardStatePlan
		if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.CardStatePlan{}, err
		}
		return plan, nil
	}
	privateOrder := func(c context.Context, stateRevision int64, request contract.InventoryOrderRequest, data []byte) (contract.OrderPage, error) {
		privateHost, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return contract.OrderPage{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.OrderPage{}, err
		}
		raw, err := privateHost.ReadServiceStream(c, revision, contract.InventoryOrder, input, rsi.InputData{ID: contract.InventoryOrderInputID, Data: data})
		if err != nil {
			return contract.OrderPage{}, err
		}
		var page contract.OrderPage
		if err := rsiplugin.DecodeResult(raw, &page); err != nil {
			return contract.OrderPage{}, err
		}
		return page, nil
	}
	return value, true, &connectedapps.DirectoryMethods{Categories: projector, Captions: captionProjector, Order: orderer, CardStates: states, InventoryOrder: privateOrder}, nil
}

func (a *App) appsPrivateReadHost(host rsi.Host, stateRevision int64) (rsi.Host, error) {
	base, ok := host.Store.(*rsistore.Store)
	if !ok || base.DB != a.store.Plugins().DB {
		return rsi.Host{}, rsi.ErrRevoked
	}
	scoped := *base
	previous := base.Fence
	scoped.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if previous != nil {
			if err := previous(ctx, tx); err != nil {
				return err
			}
		}
		var actual int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM connected_apps WHERE singleton=1`).Scan(&actual); err != nil {
			return err
		}
		if actual != stateRevision {
			return connectedapps.ErrConflict
		}
		return nil
	}
	privateHost := host
	privateHost.Store = &scoped
	return privateHost, nil
}
