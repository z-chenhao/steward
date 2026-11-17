package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/delegation/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func delegationCases() []rsi.Case {
	return []rsi.Case{{InstanceID: "delegation", Contract: contract.Contract, Input: json.RawMessage(`{"depth":2,"descendants":3}`), Expected: json.RawMessage(`{"depth":2,"descendants":3,"instructions":"Delegation: pass a self-contained subtask and accessible Artifact references. A Child receives no Parent conversation. Aggregate its final result; delegation never creates a Task."}`)}}
}
