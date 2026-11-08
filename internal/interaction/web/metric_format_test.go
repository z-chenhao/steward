package web

import "testing"

func TestWebMetricPrecisionAndUnits(t *testing.T) {
	for _, tc := range []struct {
		micros int64
		want   string
	}{{0, "0.00ms"}, {500, "0.50ms"}, {12500, "12.50ms"}, {999990, "999.99ms"}, {1000000, "1.00s"}, {8321000, "8.32s"}, {61000000, "61.00s"}} {
		if got := duration(0, &tc.micros); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	ms := int64(2106)
	if milliseconds(&ms) != "2.11s" || milliseconds(nil) != "—" || elapsed("2026-11-08T00:00:00Z", "2026-11-08T00:00:00.0125Z") != "12.50ms" {
		t.Fatal("inconsistent latency presentation")
	}
	for _, tc := range []struct {
		micros int64
		want   string
	}{{0, "0.00"}, {1, "0.01"}, {42, "0.01"}, {9999, "0.01"}, {10000, "0.01"}, {736270, "0.74"}, {1000000, "1.00"}} {
		if got := money(tc.micros); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
