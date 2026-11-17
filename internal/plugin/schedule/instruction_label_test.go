package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"testing"
)

func TestInstructionLabelPreservesBoundedOriginalSummary(t *testing.T) {
	for _, c := range []struct{ text, want string }{{"Original   instruction\nsecond line", "Original instruction second line"}, {strings.Repeat("界", 90), strings.Repeat("界", 80)}} {
		input, _ := json.Marshal(contract.InstructionLabelFacts{Text: c.text})
		out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.InstructionLabel, Input: input})
		var value contract.InstructionLabelResult
		if err != nil || rsiplugin.Decode(out.Output, &value) != nil || value.Label != c.want || out.State != nil || out.OutputData != nil {
			t.Fatal(value, err)
		}
	}
	if _, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.InstructionLabel, Input: json.RawMessage(`{"text":"original"}`), Inputs: []rsiplugin.Input{{ID: "private"}}}); err == nil {
		t.Fatal("unrelated private reference accepted")
	}
}
