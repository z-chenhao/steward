package schedule

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestNextMethodPreservesExplicitInstantAndRejectsInvalidSchedule(t *testing.T) {
	after, _ := time.Parse(time.RFC3339, "2026-11-01T01:30:00-04:00")
	for _, c := range []struct {
		name, kind, spec, zone, want, code string
	}{
		{"repeated local minute", "cron", "30 1 * * *", "America/New_York", "2026-11-01T06:30:00Z", ""},
		{"explicit instant", "at", "2026-11-09T08:00:00.123456+08:00", "Asia/Shanghai", "2026-11-09T00:00:00.123456Z", ""},
		{"implicit local zone forbidden", "cron", "* * * * *", "Local", "", "INVALID_ARGUMENT"},
		{"invalid numeric field", "cron", "60 * * * *", "UTC", "", "INVALID_ARGUMENT"},
		{"existing Unicode whitespace", "cron", "0" + strings.Repeat("\u3000", 90) + "8 * * *", "UTC", "2026-11-01T08:00:00Z", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			input, _ := json.Marshal(contract.NextFacts{Type: c.kind, Spec: c.spec, Timezone: c.zone, After: after})
			if !tool.Validate(contract.InputSchema, string(input)) {
				t.Fatal("invalid fixture schema")
			}
			result, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Next, Input: input})
			if err != nil || result.State != nil || result.OutputData != nil {
				t.Fatal("method gained state or transfer authority", err)
			}
			var actual contract.NextResult
			if err = rsiplugin.Decode(result.Output, &actual); err != nil || !tool.Validate(contract.OutputSchema, string(result.Output)) {
				t.Fatal("invalid method output", err)
			}
			if actual.Next != c.want || actual.ErrorCode != c.code {
				t.Fatal(actual, c.want, c.code)
			}
		})
	}
}

func TestNextMethodDoesNotAcceptAutomationDomainOrPrivateInputs(t *testing.T) {
	if _, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Next, Input: json.RawMessage(`{"instruction":"private"}`)}); err == nil {
		t.Fatal("internal Automation model accepted")
	}
	after, _ := time.Parse(time.RFC3339, "2026-11-01T00:00:00Z")
	input, _ := json.Marshal(contract.NextFacts{Type: "cron", Spec: "* * * * *", Timezone: "UTC", After: after})
	result, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Next, Input: input, Inputs: []rsiplugin.Input{{ID: "private-evidence"}}})
	var actual contract.NextResult
	if err != nil || rsiplugin.Decode(result.Output, &actual) != nil || actual.ErrorCode != "INVALID_ARGUMENT" || actual.Next != "" {
		t.Fatal("private input reference admitted", err)
	}
}
