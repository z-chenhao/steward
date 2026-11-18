package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func codingCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "coding-workspace", Contract: contract.ImportContract, Input: json.RawMessage(`{"ceilings":{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}}`), Expected: json.RawMessage(`{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}`)},
		{InstanceID: "coding-workspace", Contract: contract.ReportContract, Input: json.RawMessage(`{"ceilings":{"commands":256,"command_bytes":4194304,"patch_bytes":8388608}}`), Expected: json.RawMessage(`{"commands":256,"command_bytes":4194304,"patch_bytes":8388608,"order":"oldest","review_guide":["Compare the original instructions and later changes with the candidate diff; record unmet requirements.","Check candidate, environment and method identities for each receipt; a historical pass may not cover this candidate.","Inspect retained evidence, failures, unknown effects and unverified behavior before accepting the result.","Review reported time, usage and prices with missing observations left unavailable.","Confirm the exact remote revision and content before delivery; record the human acceptance decision separately."]}`)},
	}
}

func codingCheckCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "coding-workspace", Contract: contract.CheckContract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000}}`), Expected: json.RawMessage(`{"timeout_seconds":60,"output_bytes":48000}`)},
		{InstanceID: "coding-workspace", Contract: contract.CheckContract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000},"requested_timeout":5}`), Expected: json.RawMessage(`{"timeout_seconds":5,"output_bytes":48000}`)},
	}
}

func codingProjectCases() []rsi.Case {
	return []rsi.Case{{InstanceID: "coding-workspace", Contract: contract.ProjectContract, Input: json.RawMessage(`{"document":"{\"version\":1,\"name\":\"fixture\",\"prepare\":{\"command\":\"true\",\"timeout_seconds\":5},\"doctor\":{\"command\":\"true\",\"timeout_seconds\":5},\"checks\":[{\"name\":\"build\",\"requirement\":\"Build exits successfully\",\"command\":\"true\",\"timeout_seconds\":5}],\"cleanup\":{\"command\":\"true\",\"timeout_seconds\":5}}","check":"build"}`), Expected: json.RawMessage(`{"plan":{"version":1,"name":"fixture","check":"build","requirement":"Build exits successfully","prepare":{"command":"true","timeout_seconds":5},"doctor":{"command":"true","timeout_seconds":5},"verify":{"command":"true","timeout_seconds":5},"cleanup":{"command":"true","timeout_seconds":5}}}`)}}
}
