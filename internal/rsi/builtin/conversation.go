package builtin

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/conversation/contract"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Host-owned frozen baseline: the candidate does not generate its verdict.
func conversationCase() rsi.Case {
	expected, _ := json.Marshal(contract.Policy{RolePolicy: strings.TrimPrefix(prompt.Main(), agent.MainIdentity), ExecutionPolicy: strings.TrimSuffix(prompt.MainExecution(), "\n</rules>")})
	return rsi.Case{InstanceID: "conversation", Contract: contract.Contract, Input: json.RawMessage(`{"role":"main"}`), Expected: expected}
}
