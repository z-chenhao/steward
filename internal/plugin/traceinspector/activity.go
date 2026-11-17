package traceinspector

import (
	"context"
	"errors"
	"math"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func activity(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.Request
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || request.InputID != call.Inputs[0].ID {
		return rsiplugin.Result{}, errors.New("missing scoped activity facts")
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.ActivityFacts
	if err = rsiplugin.DecodeInput(raw, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	plan, err := ProjectActivity(facts)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, plan)
}

// ProjectActivity chooses presentation only. It never counts or reads records.
func ProjectActivity(f contract.ActivityFacts) (contract.ActivityPlan, error) {
	invalid := errors.New("invalid activity range")
	if (f.First == nil) != (f.Last == nil) || f.First != nil && (*f.First > *f.Last || *f.Last == math.MaxInt64) || f.Since != nil && f.Until != nil && *f.Since >= *f.Until {
		return contract.ActivityPlan{}, invalid
	}
	if f.First == nil && (f.Since == nil || f.Until == nil) {
		return contract.ActivityPlan{}, nil
	}
	var start, end int64
	if f.First != nil {
		start, end = *f.First, *f.Last+1
	} else {
		end = 1
	}
	if f.Since != nil {
		start = *f.Since
	}
	if f.Until != nil {
		end = *f.Until
	}
	if end <= start || end-start <= 0 {
		return contract.ActivityPlan{}, invalid
	}
	if f.Since == nil && f.Until == nil && end-start < 60_000_000 {
		if start > math.MaxInt64-60_000_000 {
			return contract.ActivityPlan{}, invalid
		}
		end = start + 60_000_000
	}
	span := end - start
	if span > math.MaxInt64-46 {
		return contract.ActivityPlan{}, invalid
	}
	step := activityInterval(span)
	base := start / step * step
	// Bound iteration and reject overflow before returning a recipe to the Host.
	count := 0
	for at := base; at < end; {
		if count >= 48 || at > math.MaxInt64-step {
			return contract.ActivityPlan{}, invalid
		}
		count++
		at += step
	}
	return contract.ActivityPlan{Present: true, Start: start, End: end, Base: base, Step: step}, nil
}
func activityInterval(span int64) int64 {
	for _, seconds := range []int64{1, 5, 10, 30, 60, 300, 600, 1800, 3600, 10800, 21600, 43200, 86400, 604800, 2592000, 31536000} {
		step := seconds * 1_000_000
		if step >= (span+46)/47 {
			return step
		}
	}
	return max(int64(1_000_000), (span+46)/47)
}
