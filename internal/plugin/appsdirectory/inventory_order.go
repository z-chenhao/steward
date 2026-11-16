package appsdirectory

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func inventoryOrder(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.InventoryOrderRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if request.Count < 0 || request.Offset < 0 || request.Offset > request.Count || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.InventoryOrderInputID {
		return rsiplugin.Result{}, errors.New("invalid inventory order request")
	}
	var raw bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &raw); err != nil {
		return rsiplugin.Result{}, err
	}
	facts, err := contract.DecodeInventoryOrderFacts(raw.Bytes(), request.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	indices := make([]int, len(facts))
	keys := make([]string, len(facts))
	for i, fact := range facts {
		indices[i] = i
		keys[i] = strings.ToLower(fact.Name)
	}
	less := func(i, j int) bool {
		a, b := indices[i], indices[j]
		if request.Connected && keys[a] == keys[b] {
			return facts[a].Slug < facts[b].Slug
		}
		return keys[a] < keys[b]
	}
	sort.Slice(indices, less)
	end := min(request.Count, request.Offset+contract.OrderPageSize)
	page := contract.OrderPage{Indices: indices[request.Offset:end]}
	return rsiplugin.JSONResult(ctx, page)
}
