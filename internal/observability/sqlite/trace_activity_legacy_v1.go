package sqlite

import "github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"

// Only never-enabled/direct embedders and explicit historical 1.0.1 graphs.
// Delete after those named consumers retire; current managed reads never fallback.
func legacyTraceActivityPlanV1(f contract.ActivityFacts) contract.ActivityPlan {
	if f.First == nil && (f.Since == nil || f.Until == nil) {
		return contract.ActivityPlan{}
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
	if f.Since == nil && f.Until == nil && end-start < 60_000_000 {
		end = start + 60_000_000
	}
	step := legacyTraceActivityIntervalV1(end - start)
	return contract.ActivityPlan{Present: true, Start: start, End: end, Base: start / step * step, Step: step}
}

// Keep histogram bins readable and bounded across short and long ranges.
func legacyTraceActivityIntervalV1(span int64) int64 {
	for _, seconds := range []int64{1, 5, 10, 30, 60, 300, 600, 1800, 3600, 10800, 21600, 43200, 86400, 604800, 2592000, 31536000} {
		step := seconds * 1_000_000
		if step >= (span+46)/47 {
			return step
		}
	}
	return max(int64(1_000_000), (span+46)/47)
}
