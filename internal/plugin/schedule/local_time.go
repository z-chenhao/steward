package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"time"
	"unicode/utf8"
)

func localTime(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.LocalTimeFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	value := contract.LocalTimeResult{ErrorCode: "INVALID_ARGUMENT"}
	if len(call.Inputs) != 0 || len(facts.Date) > 10 || len(facts.Clock) > 5 || utf8.RuneCountInString(facts.Timezone) > 128 || utf8.RuneCountInString(facts.OriginalSpec) > 256 {
		return rsiplugin.JSONResult(ctx, value)
	}
	if facts.OriginalSpec != "" {
		at, err := time.Parse(time.RFC3339Nano, facts.OriginalSpec)
		loc, zoneErr := calendarLocation(facts.Timezone)
		if err == nil && zoneErr == nil && at.In(loc).Format("2006-01-02") == facts.Date && at.In(loc).Format("15:04") == facts.Clock {
			return rsiplugin.JSONResult(ctx, contract.LocalTimeResult{Spec: facts.OriginalSpec})
		}
	}
	if spec, err := exactLocalOnce(facts.Date, facts.Clock, facts.Timezone); err == nil {
		value = contract.LocalTimeResult{Spec: spec}
	}
	return rsiplugin.JSONResult(ctx, value)
}

// Extracted unchanged from the existing Web editor, with the owning version's
// frozen timezone data replacing the Host's mutable system files.
func exactLocalOnce(date, clock, zone string) (string, error) {
	loc, err := calendarLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return "", errInvalid
	}
	wall, err := time.Parse("2006-01-02T15:04", date+"T"+clock)
	if err != nil {
		return "", errInvalid
	}
	offsets := map[int]bool{}
	for h := -36; h <= 36; h++ {
		_, offset := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		offsets[offset] = true
	}
	var matches []time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.In(loc).Format("2006-01-02T15:04") == date+"T"+clock {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return "", errInvalid
	}
	return matches[0].UTC().Format(time.RFC3339), nil
}
