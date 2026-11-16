package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func experienceCases() []rsi.Case {
	out := []rsi.Case{}
	for _, reason := range []string{"success", "correction", "recovered_failure"} {
		n := 1
		if reason == "success" {
			n = 2
		}
		input, _ := json.Marshal(contract.Request{Reason: reason})
		expected, _ := json.Marshal(contract.Policy{RequiredRuns: n, Introduction: ""})
		out = append(out, rsi.Case{InstanceID: "experience-learning", Contract: contract.Contract, Input: input, Expected: expected})
	}
	return out
}
