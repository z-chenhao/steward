package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"time"
	"unicode/utf8"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Next: nextTime, contract.LocalTime: localTime, contract.Frequency: frequencyTime, contract.Projection: editorProjection, contract.Display: scheduleDisplay, contract.InstructionLabel: instructionLabel, contract.Guidance: guidance, contract.Defaults: editorDefaults, contract.Threshold: thresholdDecision}}
}

func nextTime(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.NextFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	result := contract.NextResult{ErrorCode: "INVALID_ARGUMENT"}
	if len(call.Inputs) != 0 || facts.After.IsZero() || utf8.RuneCountInString(facts.Spec) > 256 || utf8.RuneCountInString(facts.Timezone) > 128 {
		return rsiplugin.JSONResult(ctx, result)
	}
	at, err := next(facts.Type, facts.Spec, facts.Timezone, facts.After)
	if err == nil {
		result = contract.NextResult{Next: at.UTC().Format(time.RFC3339Nano)}
	}
	return rsiplugin.JSONResult(ctx, result)
}
