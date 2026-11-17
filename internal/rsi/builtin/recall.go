package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func recallCases() []rsi.Case {
	makeCase := func(c string, f any, expected string) rsi.Case {
		raw, _ := json.Marshal(f)
		return rsi.Case{InstanceID: "recall", Contract: c, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: raw}}, Expected: json.RawMessage(expected)}
	}
	return []rsi.Case{
		makeCase(contract.Contract, contract.Facts{Query: "launch", EvidenceFirst: true, ExcerptBytes: 20, Candidates: []contract.Candidate{
			{Content: "launch source", SourceType: "memory", Layer: "representations", Score: 1.2, Relevance: 1, Quotes: []string{"unrelated", "launch evidence", "other"}},
			{Content: "launch original", SourceType: "conversation", Score: 1.1, Relevance: 1, Quotes: []string{}},
		}}, `{"entries":[{"index":1,"offset":0,"length":15,"evidence":[]},{"index":0,"offset":0,"length":13,"evidence":[1,0]}]}`),
		makeCase(contract.QueryContract, contract.QueryFacts{Latest: "show the result", Previous: "compare launch options", Answer: "launch evidence"}, `{"query":"show the result\nRecent conversation:\ncompare launch options\nlaunch evidence"}`),
	}
}
