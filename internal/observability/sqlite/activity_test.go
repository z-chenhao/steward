package sqlite

import "testing"

func TestActivityIntervalsRemainReadableAndBounded(t *testing.T) {
	for _, span := range []int64{1, 999, 1_000_000, 60_000_000, 86400_000_000, 365 * 86400_000_000} {
		step := activityInterval(span)
		if step < 1_000_000 || (span+step-1)/step > 48 {
			t.Fatal(span, step)
		}
	}
}
