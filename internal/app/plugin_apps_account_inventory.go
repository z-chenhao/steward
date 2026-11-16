package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readAppsAccountInventory(ctx context.Context) (connectedapps.AccountInventoryPolicy, error) {
	return a.appsAccountInventoryWithHost(ctx, a.Plugins())
}
func (a *App) appsAccountInventoryWithHost(ctx context.Context, host rsi.Host) (connectedapps.AccountInventoryPolicy, error) {
	revision, managed, err := a.store.AppsAccountInventorySelection(ctx)
	if err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	if !managed {
		return connectedapps.NativeAccountInventory(), nil
	}
	return a.appsAccountInventoryForGraph(ctx, host, revision)
}

// Selected current and retained graphs use declared protocols. The one captured
// original seven-service profile has a named historical interpreter, never a
// runtime fallback from a managed inventory execution failure.
func (a *App) appsAccountInventoryForGraph(ctx context.Context, host rsi.Host, revision string) (connectedapps.AccountInventoryPolicy, error) {
	if _, err := host.ServiceInstance(ctx, revision, contract.Inventory); err != nil {
		if errors.Is(err, rsi.ErrNotFound) {
			return a.appsHistoricalAccountInventory(ctx, host, revision)
		}
		return connectedapps.AccountInventoryPolicy{}, err
	}
	return connectedapps.ManagedAccountInventory(func(c context.Context, stateRevision int64, request contract.InventoryRequest, data []byte) (contract.InventoryPlan, error) {
		privateHost, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return contract.InventoryPlan{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.InventoryPlan{}, err
		}
		raw, err := privateHost.ReadServiceStream(c, revision, contract.Inventory, input, rsi.InputData{ID: contract.InventoryInputID, Data: data})
		if err != nil {
			return contract.InventoryPlan{}, err
		}
		var plan contract.InventoryPlan
		if err = rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.InventoryPlan{}, err
		}
		return plan, nil
	}), nil
}

// These Host-owned protocol snapshots name actual pre-inventory default1.0
// consumers. It is not a version-only classifier or a generic legacy framework.
// Delete it with the original interpreter once these old graphs/references retire.
//
//go:embed legacy-apps-account-inventory-profiles.json
var historicalAccountInventoryManifests []byte

func (a *App) appsHistoricalAccountInventory(ctx context.Context, host rsi.Host, revision string) (connectedapps.AccountInventoryPolicy, error) {
	binding, err := host.ServiceInstance(ctx, revision, contract.ConnectIntent)
	if err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	var profiles []struct {
		Consumer string             `json:"consumer"`
		Manifest rsiplugin.Manifest `json:"manifest"`
	}
	if err = rsiplugin.Decode(historicalAccountInventoryManifests, &profiles); err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	actual := pkg.Manifest
	// Payload hashes differ by carrier; Catalog.Load verifies the sealed bytes.
	// Compare all protocol/schema/permission metadata, not a version tag alone.
	actual.Files = nil
	raw, err := rsiplugin.Canonical(actual)
	if err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	matched := false
	for _, profile := range profiles {
		original := profile.Manifest
		original.Files = nil
		expected, e := rsiplugin.Canonical(original)
		if e != nil {
			return connectedapps.AccountInventoryPolicy{}, e
		}
		if bytes.Equal(raw, expected) {
			matched = true
			break
		}
	}
	if !matched {
		return connectedapps.AccountInventoryPolicy{}, rsi.ErrNotFound
	}
	if err = host.Store.CheckGraph(ctx, revision); err != nil {
		return connectedapps.AccountInventoryPolicy{}, err
	}
	return connectedapps.HistoricalAccountInventory(func(c context.Context, stateRevision int64) error {
		scoped, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return err
		}
		return scoped.Store.CheckGraph(c, revision)
	}), nil
}
