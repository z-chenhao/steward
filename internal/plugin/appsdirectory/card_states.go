package appsdirectory

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func cardStates(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.CardStateRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if request.Count < 0 || request.Offset < 0 || request.Offset > request.Count || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.CardStateInputID || call.Inputs[0].Size > contract.CardStateWindow {
		return rsiplugin.Result{}, errors.New("invalid card state request")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	if len(raw) > request.Count-request.Offset {
		return rsiplugin.Result{}, errors.New("invalid card state window")
	}
	for _, ref := range []*int{request.Prior.Managed, request.Prior.Active, request.Prior.Pending} {
		if ref != nil && (*ref < 0 || *ref >= request.Offset) {
			return rsiplugin.Result{}, errors.New("invalid prior state reference")
		}
	}
	plan := request.Prior
	for i, fact := range raw {
		if fact&^byte(31) != 0 {
			return rsiplugin.Result{}, errors.New("invalid card fact")
		}
		row := request.Offset + i
		if plan.Managed == nil && (fact&contract.FactDisconnected == 0 || fact&contract.FactRevocationConfirmed == 0) {
			value := row
			plan.Managed = &value
		}
		if fact&contract.FactActive != 0 {
			value := row
			plan.Active = &value
		}
		if plan.Pending == nil && fact&contract.FactPending != 0 {
			value := row
			plan.Pending = &value
		}
	}
	return rsiplugin.JSONResult(ctx, plan)
}
