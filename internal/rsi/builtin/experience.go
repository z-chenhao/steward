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
	// These two independent owner cases check a matching sample and a wrong goal.
	step := contract.Step{Action: "activate", Label: "Preview"}
	for _, goal := range []int{1, 2} {
		input, _ := json.Marshal(contract.DemoRequest{Name: "Preview report", Steps: []contract.Step{step}, Sample: []contract.Transition{{From: 0, Step: step, To: 1}}, Goal: goal})
		expected, _ := json.Marshal(contract.DemoResult{Steps: []string{`1. Activate "Preview".`}, Matched: 1, Verified: goal == 1})
		out = append(out, rsi.Case{InstanceID: "experience-learning", Contract: contract.Demonstration, Input: input, Expected: expected})
	}
	return out
}
