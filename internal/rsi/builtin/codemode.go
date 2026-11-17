package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/codemodepolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func codeModeCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "codemode", Contract: contract.LimitsContract, Input: json.RawMessage(`{"role":"main","ceilings":{"timeout_ms":30000,"memory_bytes":33554432,"output_bytes":65536,"pending_calls":128,"interrupt_budget":100000}}`), Expected: json.RawMessage(`{"limits":{"timeout_ms":30000,"memory_bytes":33554432,"output_bytes":65536,"pending_calls":128,"interrupt_budget":100000}}`)},
		{InstanceID: "codemode", Contract: contract.FormatContract, Input: json.RawMessage(`{"input_id":"schema"}`), Inputs: []rsi.InputData{{ID: "schema", Data: []byte(`{"type":"object","properties":{"q":{"type":"string"},"count":{"type":"integer"}},"required":["q"],"additionalProperties":false}`)}}, Expected: json.RawMessage(`{"input_type":"{ \"count\"?: number; \"q\": string }","guidance":""}`)},
	}
}
