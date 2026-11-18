package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"sort"
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
	loc, zoneErr := calendarLocation(facts.Timezone)
	if zoneErr != nil {
		value.Field = "timezone"
		return rsiplugin.JSONResult(ctx, value)
	}
	if facts.OriginalSpec != "" {
		at, err := time.Parse(time.RFC3339Nano, facts.OriginalSpec)
		local := at.In(loc)
		if err == nil && local.Format("2006-01-02") == facts.Date && local.Format("15:04") == facts.Clock && (facts.UTCOffset == "" || facts.UTCOffset == local.Format("-07:00")) {
			return rsiplugin.JSONResult(ctx, contract.LocalTimeResult{Spec: facts.OriginalSpec})
		}
	}
	if _, err := time.Parse("2006-01-02", facts.Date); err != nil {
		value.Field = "date"
		return rsiplugin.JSONResult(ctx, value)
	}
	wall, err := time.Parse("2006-01-02T15:04", facts.Date+"T"+facts.Clock)
	if err != nil {
		value.Field = "time"
		return rsiplugin.JSONResult(ctx, value)
	}
	matches := matchingInstants(wall, loc)
	for _, instant := range matches {
		if (len(matches) == 1 && facts.UTCOffset == "") || (facts.UTCOffset != "" && facts.UTCOffset == instant.In(loc).Format("-07:00")) {
			return rsiplugin.JSONResult(ctx, contract.LocalTimeResult{Spec: instant.UTC().Format(time.RFC3339)})
		}
	}
	value.Field = "time"
	if len(matches) > 0 {
		value.Field = "utc_offset"
		for _, instant := range matches {
			value.UTCOffsets = append(value.UTCOffsets, instant.In(loc).Format("-07:00"))
		}
	}
	return rsiplugin.JSONResult(ctx, value)
}

// Candidate offsets come from the package's frozen timezone archive. Chronological
// ordering keeps the first and second occurrences stable across processes.
func matchingInstants(wall time.Time, loc *time.Location) []time.Time {
	offsets := map[int]bool{}
	for h := -36; h <= 36; h++ {
		_, offset := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		offsets[offset] = true
	}
	var matches []time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.In(loc).Format("2006-01-02T15:04") == wall.Format("2006-01-02T15:04") {
			matches = append(matches, candidate)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Before(matches[j]) })
	return matches
}
