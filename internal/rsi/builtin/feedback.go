package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func feedbackCases(version string) []rsi.Case {
	expected, _ := json.Marshal(contract.Policy{
		SeparateKinds: version == "1.0.2", LifetimeSeconds: 604800,
		ResponseGuidance: "If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;",
	})
	return []rsi.Case{
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"input"}`), Expected: expected},
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"review"}`), Expected: expected},
	}
}
