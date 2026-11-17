package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func fileCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "file-tools", Contract: contract.WindowContract, Input: json.RawMessage(`{"input_id":"file","offset":1,"limit":2,"output_bytes":48000,"as_artifact":false}`), Inputs: []rsi.InputData{{ID: "file", Data: []byte("first\nsecond\n")}}, Expected: json.RawMessage(`{"artifact":false,"start":0,"end":13,"next_offset":3}`)},
		{InstanceID: "file-tools", Contract: contract.EditContract, Input: json.RawMessage(`{"input_id":"file","old_text":"OLD"}`), Inputs: []rsi.InputData{{ID: "file", Data: []byte("prefix OLD tail")}}, Expected: json.RawMessage(`{"start":7}`)},
	}
}
