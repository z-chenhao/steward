package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/skilllibrary/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func skillCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "skill-library", Contract: contract.SearchContract, Input: json.RawMessage(`{"query":"","limit":8,"entries":[{"name":"zeta","description":"research"},{"name":"alpha","description":"coding"}]}`), Expected: json.RawMessage(`{"indices":[1,0]}`)},
		{InstanceID: "skill-library", Contract: contract.ResourceContract, Input: json.RawMessage(`{"path":""}`), Expected: json.RawMessage(`{"path":"SKILL.md"}`)},
		{InstanceID: "skill-library", Contract: contract.ResourceContract, Input: json.RawMessage(`{"path":"references/details.md"}`), Expected: json.RawMessage(`{"path":"references/details.md"}`)},
	}
}
