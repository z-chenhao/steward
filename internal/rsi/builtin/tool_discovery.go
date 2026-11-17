package builtin

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Owner expectations are independent literal outcomes. No live capability,
// account,credential,model or candidate-generated expected value enters these.
func toolDiscoveryCases() []rsi.Case {
	type scenario struct {
		query      string
		limit      int
		candidates []contract.Candidate
		entries    []contract.Entry
	}
	scenarios := []scenario{
		{"create_event", 1, []contract.Candidate{{Name: "mcp__calendar__list_events", Description: "List events"}, {Name: "mcp__calendar__create_event", Description: "Create events"}}, []contract.Entry{{Index: 1, Length: 13}}},
		{"contract renewal", 1, []contract.Candidate{{Name: "mcp__docs__lookup", Description: "Read item", Schema: `{"type":"object","properties":{"document":{"description":"contract renewal"}}}`}, {Name: "mcp__other__lookup", Description: "Other task"}}, []contract.Entry{{Index: 0, Length: 9}}},
		{"reimbursement", 1, []contract.Candidate{{Name: "mcp__finance__lookup", Description: "Find tax", Namespace: "finance", NamespaceDescription: "Expense reimbursement"}, {Name: "mcp__other__lookup", Description: "Other task"}}, []contract.Entry{{Index: 0, Length: 8}}},
		{"日历", 1, []contract.Candidate{{Name: "mcp__chinese__lookup", Description: "查询日历事件"}}, []contract.Entry{{Index: 0, Length: 18}}},
		{"catalog", 1, []contract.Candidate{{Name: "mcp__catalog__a", Description: "Find a record"}, {Name: "mcp__catalog__b", Description: "Find a record"}}, []contract.Entry{{Index: 0, Length: 13}}},
		{"lookup", 1, []contract.Candidate{{Name: "mcp__docs__lookup", Description: "  " + strings.Repeat("界", 300) + "  "}}, []contract.Entry{{Index: 0, Offset: 2, Length: 768, Ellipsis: true}}},
		{"the and of", 8, []contract.Candidate{{Name: "mcp__catalog__a", Description: "Find a record"}}, []contract.Entry{}},
		{"unmatched", 8, []contract.Candidate{}, []contract.Entry{}},
	}
	cases := make([]rsi.Case, 0, len(scenarios))
	for _, s := range scenarios {
		input, _ := json.Marshal(contract.Request{Count: len(s.candidates), Limit: s.limit})
		expected, _ := json.Marshal(contract.Plan{Entries: s.entries})
		cases = append(cases, rsi.Case{InstanceID: "tool-discovery", Contract: contract.Contract, Input: input, Inputs: []rsi.InputData{{ID: contract.InputID, Data: contract.Encode(contract.Facts{Query: s.query, Candidates: s.candidates})}}, Expected: expected})
	}
	return cases
}
