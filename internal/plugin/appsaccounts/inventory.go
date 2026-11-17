package appsaccounts

import (
	"bytes"
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"sort"
	"strings"
)

func inventory(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.InventoryRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.InventoryInputID {
		return rsiplugin.Result{}, errors.New("missing account inventory facts")
	}
	var raw bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &raw); err != nil {
		return rsiplugin.Result{}, err
	}
	rows, err := contract.DecodeInventoryRows(raw.Bytes(), request.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	items := chooseInventory(rows, request.Connected)
	if request.Offset < 0 || request.Offset > len(items) {
		return rsiplugin.Result{}, errors.New("invalid account inventory window")
	}
	end := min(len(items), request.Offset+contract.InventoryPageSize)
	return rsiplugin.JSONResult(ctx, contract.InventoryPlan{Total: len(items), Items: items[request.Offset:end]})
}
func chooseInventory(rows []contract.InventoryRow, connected bool) []contract.InventoryItem {
	items := make([]contract.InventoryItem, 0)
	groups := map[string]int{}
	for i, row := range rows {
		if row.Facts&contract.Disconnected != 0 && row.Facts&contract.RevocationConfirmed != 0 {
			continue
		}
		if _, ok := groups[row.Slug]; ok {
			continue
		}
		groups[row.Slug] = len(items)
		items = append(items, contract.InventoryItem{Row: i})
	}
	for i, row := range rows {
		at, ok := groups[row.Slug]
		if !ok {
			continue
		}
		if row.Facts&contract.Active != 0 {
			v := i
			items[at].Active = &v
		}
		if row.Facts&contract.Pending != 0 {
			v := i
			items[at].Pending = &v
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := rows[items[i].Row], rows[items[j].Row]
		x, y := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if connected && x == y {
			return a.Slug < b.Slug
		}
		return x < y
	})
	return items
}
