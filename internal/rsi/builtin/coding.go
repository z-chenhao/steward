package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func codingCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "coding-workspace", Contract: contract.ImportContract, Input: json.RawMessage(`{"ceilings":{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}}`), Expected: json.RawMessage(`{"entries":4096,"file_bytes":8388608,"tree_bytes":67108864}`)},
		{InstanceID: "coding-workspace", Contract: contract.ReportContract, Input: json.RawMessage(`{"ceilings":{"commands":256,"command_bytes":4194304,"patch_bytes":8388608}}`), Expected: json.RawMessage(`{"commands":256,"command_bytes":4194304,"patch_bytes":8388608,"order":"oldest"}`)},
	}
}
