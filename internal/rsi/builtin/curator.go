package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/memorycurator/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func curatorCases() []rsi.Case {
	out := []rsi.Case{}
	for _, test := range []struct{ Style, Instructions string }{{"facts", memory.CurationInstruction}, {"dream", memory.DreamInstruction}, {"grounded", memory.GroundedDreamInstruction}, {"worker", memory.WorkerDreamInstruction}} {
		input, _ := json.Marshal(contract.Request{Style: test.Style})
		expected, _ := json.Marshal(contract.Policy{Instructions: test.Instructions})
		out = append(out, rsi.Case{InstanceID: "memory-curator", Contract: contract.Contract, Input: input, Expected: expected})
	}
	out = append(out, rsi.Case{InstanceID: "memory-curator", Contract: contract.ScheduleContract, Input: json.RawMessage(`{}`), Expected: json.RawMessage(`{"min_changes":8,"max_wait_seconds":300}`)})
	return out
}
