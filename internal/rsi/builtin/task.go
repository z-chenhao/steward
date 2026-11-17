package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func taskCases() []rsi.Case {
	facts, _ := json.Marshal(contract.SearchFacts{Query: "", Limit: 2, Candidates: []contract.Candidate{{Text: "retained original", Status: "paused", UpdatedAt: "2026-01-01"}, {Text: "current target", Status: "active", UpdatedAt: "2026-01-01"}}})
	return []rsi.Case{
		{InstanceID: "task-control", Contract: contract.SearchContract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: facts}}, Expected: json.RawMessage(`{"indices":[1,0]}`)},
		{InstanceID: "task-control", Contract: contract.LabelContract, Input: json.RawMessage(`{"text":"default   task\nbrief","explicit":false}`), Expected: json.RawMessage(`{"title":"default task brief"}`)},
		{InstanceID: "task-control", Contract: contract.LabelContract, Input: json.RawMessage(`{"text":"Chosen title","explicit":true}`), Expected: json.RawMessage(`{"title":"Chosen title"}`)},
	}
}
