package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func thresholdDecision(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.ThresholdFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errInvalid
	}
	result, err := thresholdTransition(facts)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, result)
}

// thresholdTransition owns the existing below-entry policy. The Host supplies
// freshness from authoritative fire ordering and accepts the transition atomically.
func thresholdTransition(facts contract.ThresholdFacts) (contract.ThresholdResult, error) {
	if facts.Value < 0 || facts.Value > 1000000000000 || facts.Below < 0 || facts.Below > 1000000000000 {
		return contract.ThresholdResult{}, errInvalid
	}
	if !facts.Fresh {
		return contract.ThresholdResult{IsBelow: facts.Entered}, nil
	}
	below := facts.Value < facts.Below
	return contract.ThresholdResult{IsBelow: below, Notify: below && !facts.Entered}, nil
}
