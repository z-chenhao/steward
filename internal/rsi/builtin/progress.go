package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/taskprogress/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func progressCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "task-progress", Contract: contract.GuidanceContract, Input: json.RawMessage(`{"role":"root"}`), Expected: json.RawMessage(`{"instructions":"- Before tools, when the execution stage changes, write one brief progress sentence in assistant text, at most 512 UTF-8 bytes. State confirmed progress and the next stage; omit private reasoning and raw tool calls. Final text remains the Task result."}`)},
		{InstanceID: "task-progress", Contract: contract.SelectionContract, Input: json.RawMessage(`{"summary":"Sources checked; comparing options."}`), Expected: json.RawMessage(`{"publish":true}`)},
	}
}
