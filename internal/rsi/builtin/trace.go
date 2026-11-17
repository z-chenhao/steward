package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func traceCase(legacy bool) rsi.Case {
	end, childEnd := int64(2000000), int64(1600000)
	facts := contract.Facts{Started: 1000000, Ended: &end, Now: 3000000, SelectedType: "generation", Items: []contract.Observation{
		{ID: "root", Started: 1000000, Ended: &end},
		{ID: "child", Parent: "root", Started: 1200000, Ended: &childEnd},
	}, Timings: []contract.Timing{{Attempt: "first", Phase: "started", At: 1000000}, {Attempt: "retry", Phase: "started", At: 1001000}, {Attempt: "retry", Phase: "first_text", At: 1002000}}}
	if legacy {
		raw, _ := json.Marshal(facts)
		return rsi.Case{InstanceID: "trace-inspector", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: raw}}, Expected: json.RawMessage(`{"nodes":[{"id":"root","depth":0,"parent_outside":false,"has_children":true,"guides":[],"x":0,"width":1000},{"id":"child","depth":1,"parent_outside":false,"has_children":false,"guides":[false],"x":200,"width":400}],"ttft_ms":2,"tabs":["overview","context","wire","diff"]}`)}
	}
	raw, _ := json.Marshal(facts)
	return rsi.Case{InstanceID: "trace-inspector", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: raw}}, Expected: json.RawMessage(`{"nodes":[{"id":"root","depth":0,"parent_outside":false,"has_children":true,"guides":[],"x":0,"width":1000,"primary":true,"primary_has_children":true},{"id":"child","depth":1,"parent_outside":false,"has_children":false,"guides":[false],"x":200,"width":400,"primary":true}],"ttft_ms":2,"tabs":["overview","context","wire","diff"],"timeline":{"start":1000000,"end":2000000},"first_output_at":1002000}`)}
}

func traceDiffCase() rsi.Case {
	facts := contract.DiffFacts{Section: "messages", Before: []contract.Record{{Index: 0, Label: "user", SourceRef: "source", ValueKnown: true, Digest: "before"}}, AfterRecords: []contract.Record{{Index: 0, Label: "user", SourceRef: "source", ValueKnown: true, Digest: "after"}}}
	raw, _ := json.Marshal(facts)
	return rsi.Case{InstanceID: "trace-inspector", Contract: contract.DiffContract, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: raw}}, Expected: json.RawMessage(`{"items":[{"kind":"changed","before":0,"after":0,"label":"user"}]}`)}
}

func traceToolCase() rsi.Case {
	raw, _ := json.Marshal(contract.ToolFacts{Name: "builtin__codemode_execute", Available: true, Complete: true, Content: `{"text":"[{\"type\":\"text\",\"text\":\"trusted receipt fixture\"}]","artifact_refs":["artifact:source"]}`})
	return rsi.Case{InstanceID: "trace-inspector", Contract: contract.ToolContract, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: raw}}, Expected: json.RawMessage(`{"texts":["trusted receipt fixture"],"artifacts":["artifact:source"],"images":[]}`)}
}

// Independently reviewed candidate resource bytes. Original semantic equivalence
// is checked separately against the pre-migration executed TypeScript oracle.
//
//go:embed trace-workbench-owner-v1.json
var traceWorkbenchAcceptance []byte

func traceWorkbenchCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(traceWorkbenchAcceptance, &cases)
	return cases, err
}
