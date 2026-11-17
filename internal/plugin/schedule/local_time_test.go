package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestLocalTimeMethodPreservesEditorDSTRules(t *testing.T) {
	for _, c := range []struct{ date, clock, zone, spec string }{
		{"2030-11-04", "08:30", "Asia/Shanghai", "2030-11-04T00:30:00Z"},
		{"2026-03-08", "02:30", "America/New_York", ""},
		{"2026-11-01", "01:30", "America/New_York", ""},
		{"2026-11-01", "02:30", "America/New_York", "2026-11-01T07:30:00Z"},
		{"2026-02-29", "08:30", "UTC", ""},
		{"2030-11-04", "08:30", "Local", ""},
	} {
		t.Run(c.date+c.clock+c.zone, func(t *testing.T) {
			input, _ := json.Marshal(contract.LocalTimeFacts{Date: c.date, Clock: c.clock, Timezone: c.zone})
			if !tool.Validate(contract.LocalTimeInputSchema, string(input)) {
				t.Fatal("invalid test input")
			}
			out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.LocalTime, Input: input})
			if err != nil || out.State != nil || out.OutputData != nil {
				t.Fatal("local-time method gained effect/state", err)
			}
			var result contract.LocalTimeResult
			if err = rsiplugin.Decode(out.Output, &result); err != nil || !tool.Validate(contract.LocalTimeOutputSchema, string(out.Output)) {
				t.Fatal("invalid output", err)
			}
			code := ""
			if c.spec == "" {
				code = "INVALID_ARGUMENT"
			}
			if result.Spec != c.spec || result.ErrorCode != code {
				t.Fatal(result, c.spec, code)
			}
		})
	}
}

func TestLocalTimeMethodRejectsPrivateDomainAndInputs(t *testing.T) {
	if _, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.LocalTime, Input: json.RawMessage(`{"instruction":"private"}`)}); err == nil {
		t.Fatal("domain input accepted")
	}
	out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.LocalTime, Input: json.RawMessage(`{"date":"2030-11-04","clock":"08:30","timezone":"UTC"}`), Inputs: []rsiplugin.Input{{ID: "private"}}})
	var result contract.LocalTimeResult
	if err != nil || rsiplugin.Decode(out.Output, &result) != nil || result.ErrorCode != "INVALID_ARGUMENT" || result.Spec != "" {
		t.Fatal("private reference accepted", err)
	}
}

func TestLocalTimeMethodPreservesAuthenticatedOriginalPrecisionAndFold(t *testing.T) {
	for _, spec := range []string{"2026-11-01T01:30:45.123456789-04:00", "2026-11-01T01:30:45.123456789-05:00", "2026-11-01T01:30:45.123456789012345678901234567890-05:00"} {
		input, _ := json.Marshal(contract.LocalTimeFacts{Date: "2026-11-01", Clock: "01:30", Timezone: "America/New_York", OriginalSpec: spec})
		out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.LocalTime, Input: input})
		var value contract.LocalTimeResult
		if err != nil || rsiplugin.Decode(out.Output, &value) != nil || value.ErrorCode != "" || value.Spec != spec {
			t.Fatal("original fold instant normalized or lost precision", value, err)
		}
	}
}
