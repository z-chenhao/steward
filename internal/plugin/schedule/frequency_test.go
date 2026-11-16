package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestFrequencyMethodPreservesExistingEditorRules(t *testing.T) {
	for _, c := range []struct{ frequency, clock, weekday, day, spec string }{
		{"daily", "08:30", "", "", "30 8 * * *"},
		{"weekdays", "08:30", "", "", "30 8 * * 1-5"},
		{"weekly", "08:30", "0", "", "30 8 * * 0"},
		{"monthly", "08:30", "", "31", "30 8 31 * *"},
		{"weekly", "08:30", "7", "", ""},
		{"monthly", "08:30", "", "0", ""},
		{"daily", "25:00", "", "", ""},
		{"unknown", "08:30", "", "", ""},
	} {
		input, _ := json.Marshal(contract.FrequencyFacts{Frequency: c.frequency, Clock: c.clock, Weekday: c.weekday, Day: c.day})
		if !tool.Validate(contract.FrequencyInputSchema, string(input)) {
			t.Fatal("invalid test input")
		}
		out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Frequency, Input: input})
		var result contract.FrequencyResult
		if err != nil || rsiplugin.Decode(out.Output, &result) != nil || !tool.Validate(contract.FrequencyOutputSchema, string(out.Output)) || out.State != nil || out.OutputData != nil {
			t.Fatal("invalid/effectful method", err)
		}
		code := ""
		if c.spec == "" {
			code = "INVALID_ARGUMENT"
		}
		if result.Spec != c.spec || result.ErrorCode != code {
			t.Fatal(c, result)
		}
	}
}
