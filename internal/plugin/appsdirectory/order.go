package appsdirectory

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"sort"
	"strings"
)

func catalogueOrder(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.OrderRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if request.Count < 0 || request.Count > rsiplugin.MaxInputBytes/2 || request.Offset < 0 || request.Offset > request.Count || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.OrderInputID {
		return rsiplugin.Result{}, errors.New("invalid catalogue order request")
	}
	for _, plan := range []contract.LabelPlan{request.Labels.Named, request.Labels.Unnamed} {
		if plan.NameSource != "name" && plan.NameSource != "slug" {
			return rsiplugin.Result{}, errors.New("invalid label source")
		}
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	facts, err := contract.DecodeOrderFacts(raw, request.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	indices := make([]int, len(facts))
	keys := make([]string, len(facts))
	for i, fact := range facts {
		indices[i] = i
		plan := request.Labels.Unnamed
		if fact.Name != "" {
			plan = request.Labels.Named
		}
		name := fact.Name
		if plan.NameSource == "slug" {
			name = fact.Slug
		}
		keys[i] = strings.ToLower(name)
	}
	less := func(i, j int) bool {
		a, b := indices[i], indices[j]
		if keys[a] == keys[b] {
			return facts[a].Slug < facts[b].Slug
		}
		return keys[a] < keys[b]
	}
	sort.Slice(indices, less)
	end := min(request.Count, request.Offset+contract.OrderPageSize)
	page := contract.OrderPage{Indices: indices[request.Offset:end]}
	return rsiplugin.JSONResult(ctx, page)
}
