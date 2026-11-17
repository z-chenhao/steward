package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/prompt"

	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Synthetic costs and expected choices are Host-authored. No candidate selector
// is called to derive its own acceptance verdict.
func contextCases() []rsi.Case {
	makeCase := func(f contract.Facts, expected string) rsi.Case {
		raw, _ := json.Marshal(f)
		return rsi.Case{InstanceID: "context-compact", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: raw}}, Expected: json.RawMessage(expected)}
	}
	f := contract.Facts{ModelBytes: 5000, FrameBytes: 20000, Recent: 2, SummaryBytes: 256, ImageLimit: 8, Messages: []contract.Message{
		{Role: "system", Protected: true, Full: contract.Cost{Model: 100, Frame: 100}, Images: []contract.Image{}},
		{Role: "user", Input: true, Full: contract.Cost{Model: 6000, Frame: 6000}, Images: []contract.Image{}},
		{Role: "assistant", Full: contract.Cost{Model: 100, Frame: 100}, Images: []contract.Image{}},
		{Role: "user", Input: true, Protected: true, Full: contract.Cost{Model: 100, Frame: 100}, Images: []contract.Image{}},
	}}
	expected, _ := json.Marshal(contract.Plan{Images: [][]int{{}, {}, {}, {}}, Through: 2, Instructions: prompt.SummaryInstructions(256)})
	cases := []rsi.Case{makeCase(f, string(expected))}
	f.Messages = f.Messages[:1]
	f.Messages[0].Full = contract.Cost{Model: 8000, Frame: 8000}
	cases = append(cases, makeCase(f, `{"images":[[]],"summarize_through":0,"error_code":"CONTEXT_LENGTH_EXCEEDED"}`))
	return cases
}
