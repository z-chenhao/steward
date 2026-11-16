package builtin

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/evidencereader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func evidenceCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "evidence-reader", Contract: contract.TextContract, Input: json.RawMessage(`{"bytes":8,"boundaries":[0,3,6,7,8],"breaks":[7]}`), Expected: json.RawMessage(`{"bytes":8}`)},
		{InstanceID: "evidence-reader", Contract: contract.TextContract, Input: json.RawMessage(`{"bytes":0,"boundaries":[0],"breaks":[]}`), Expected: json.RawMessage(`{"bytes":0}`)},
		{InstanceID: "evidence-reader", Contract: contract.LinksContract, Input: json.RawMessage(`{"candidates":21}`), Expected: json.RawMessage(`{"take":20}`)},
	}
}
