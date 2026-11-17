package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func feedbackCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"input"}`), Expected: json.RawMessage(`{"lifetime_seconds":604800,"response_guidance":"If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"}`)},
		{InstanceID: "task-feedback", Contract: contract.Contract, Input: json.RawMessage(`{"kind":"review"}`), Expected: json.RawMessage(`{"lifetime_seconds":604800,"response_guidance":"If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"}`)},
	}
}
