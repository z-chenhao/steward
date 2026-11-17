package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/shellpolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func shellCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "shell-tools", Contract: contract.Contract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000}}`), Expected: json.RawMessage(`{"timeout_seconds":60,"output_bytes":48000}`)},
		{InstanceID: "shell-tools", Contract: contract.Contract, Input: json.RawMessage(`{"ceilings":{"timeout_seconds":120,"output_bytes":48000},"requested_timeout":5}`), Expected: json.RawMessage(`{"timeout_seconds":5,"output_bytes":48000}`)},
	}
}
