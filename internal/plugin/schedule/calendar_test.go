package schedule

import (
	"testing"
	"time"
)

func TestCalendarBoundaries(t *testing.T) {
	for _, c := range []struct{ name, spec, zone, after, want string }{
		{"wildcard day step", "0 8 */1 * 0", "UTC", "2026-10-15T09:00:00Z", "2026-10-18T08:00:00Z"},
		{"wildcard weekday step", "0 8 1 * */1", "UTC", "2026-10-15T09:00:00Z", "2026-11-01T08:00:00Z"},
		{"restricted day OR weekday", "0 8 1 * 5", "UTC", "2026-10-15T09:00:00Z", "2026-10-16T08:00:00Z"},
		{"century leap gap", "0 0 29 2 *", "UTC", "2096-02-29T00:00:00Z", "2104-02-29T00:00:00Z"},
		{"GC02", "0 20 * * 0L", "Asia/Shanghai", "2026-10-25T21:00:00+08:00", "2026-11-29T20:00:00+08:00"},
		{"GC14", "0 8 * * *", "Asia/Shanghai", "2026-10-15T12:00:00+08:00", "2026-10-16T08:00:00+08:00"},
		{"spring skips missing minute", "30 2 * * *", "America/New_York", "2026-03-07T03:00:00-05:00", "2026-03-09T02:30:00-04:00"},
		{"fall first", "30 1 * * *", "America/New_York", "2026-10-31T23:59:00-04:00", "2026-11-01T01:30:00-04:00"},
		{"fall second", "30 1 * * *", "America/New_York", "2026-11-01T01:30:00-04:00", "2026-11-01T01:30:00-05:00"},
		{"month boundary", "0 8 1 11 *", "Asia/Shanghai", "2026-10-31T23:00:00+08:00", "2026-11-01T08:00:00+08:00"},
		{"leap day", "0 0 29 2 *", "UTC", "2025-03-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"range step list", "0,30 8-10/2 * * *", "UTC", "2026-10-15T08:30:00Z", "2026-10-15T10:00:00Z"},
	} {
		t.Run(c.name, func(t *testing.T) {
			after, _ := time.Parse(time.RFC3339, c.after)
			want, _ := time.Parse(time.RFC3339, c.want)
			got, e := next("cron", c.spec, c.zone, after)
			if e != nil || !got.Equal(want) {
				t.Fatalf("got %s %v want %s", got, e, want)
			}
		})
	}
}
