package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestEditorProjectionPreservesExistingFields(t *testing.T) {
	for _, c := range []struct{ kind, spec, zone, frequency, date, clock, weekday, day string }{
		{"at", "2026-11-01T01:30:45.123456789-05:00", "America/New_York", "once", "2026-11-01", "01:30", "", ""},
		{"cron", "30 8 * * *", "UTC", "daily", "", "08:30", "", ""},
		{"cron", "30 8 * * 1-5", "UTC", "weekdays", "", "08:30", "", ""},
		{"cron", "30 8 * * 0", "UTC", "weekly", "", "08:30", "0", ""},
		{"cron", "30 8 31 * *", "UTC", "monthly", "", "08:30", "", "31"},
		{"cron", "*/15 9-17 * * 1-5", "UTC", "custom", "", "08:00", "", ""},
	} {
		input, _ := json.Marshal(contract.ProjectionFacts{Type: c.kind, Spec: c.spec, Timezone: c.zone})
		out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Projection, Input: input})
		var actual contract.ProjectionResult
		expected := contract.ProjectionResult{Frequency: c.frequency, Date: c.date, Clock: c.clock, Weekday: c.weekday, Day: c.day}
		if err != nil || rsiplugin.Decode(out.Output, &actual) != nil || actual != expected || !tool.Validate(contract.ProjectionOutputSchema, string(out.Output)) || out.State != nil || out.OutputData != nil {
			t.Fatal(c, actual, err)
		}
	}
}
