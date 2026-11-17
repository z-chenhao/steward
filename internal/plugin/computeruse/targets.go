package computeruse

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func targetInput(ctx context.Context, call rsiplugin.Call, dst any) error {
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "targets" || call.Inputs[0].Size > 8<<20 {
		return errors.New("missing bounded target labels")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return err
	}
	return rsiplugin.Decode(raw, dst)
}
func orderInventory(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.InventoryFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	var input contract.InventoryLabels
	if err := targetInput(ctx, call, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	indices := make([]int, len(input.Labels))
	for i := range indices {
		indices[i] = i
	}
	return rsiplugin.JSONResult(ctx, contract.InventoryOrder{Indices: indices})
}
func matchTargets(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.MatchFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	var input contract.MatchLabels
	if err := targetInput(ctx, call, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	indices := []int{}
	for i, label := range input.Labels {
		if input.Selector == "" && facts.Kind == "browsers" || label.IDMatch || label.Name.Complete && input.Selector == label.Name.Text || input.Selector != "" && label.URL.Complete && input.Selector == label.URL.Text {
			indices = append(indices, i)
		}
	}
	return rsiplugin.JSONResult(ctx, contract.TargetMatches{Indices: indices})
}
