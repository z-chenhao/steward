package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strconv"
	"time"
)

func frequencyTime(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.FrequencyFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	value := contract.FrequencyResult{ErrorCode: "INVALID_ARGUMENT"}
	if len(call.Inputs) == 0 && len(facts.Frequency) <= 16 && len(facts.Clock) <= 5 && len(facts.Weekday) <= 2 && len(facts.Day) <= 2 {
		if spec, err := frequencyExpression(facts.Frequency, facts.Clock, facts.Weekday, facts.Day); err == nil {
			value = contract.FrequencyResult{Spec: spec}
		}
	}
	return rsiplugin.JSONResult(ctx, value)
}
func frequencyExpression(frequency, clock, weekday, day string) (string, error) {
	at, err := time.Parse("15:04", clock)
	if err != nil {
		return "", errInvalid
	}
	dom, dow := "*", "*"
	switch frequency {
	case "daily":
	case "weekdays":
		dow = "1-5"
	case "weekly":
		n, err := strconv.Atoi(weekday)
		if err != nil || n < 0 || n > 6 {
			return "", errInvalid
		}
		dow = strconv.Itoa(n)
	case "monthly":
		n, err := strconv.Atoi(day)
		if err != nil || n < 1 || n > 31 {
			return "", errInvalid
		}
		dom = strconv.Itoa(n)
	default:
		return "", errInvalid
	}
	return strconv.Itoa(at.Minute()) + " " + strconv.Itoa(at.Hour()) + " " + dom + " * " + dow, nil
}
