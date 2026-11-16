package builtin

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// These expectations belong to the owner. They are not observations obtained
// by running a candidate, and verification cannot perform an actual peer call.
func mcpCases() []rsi.Case {
	method := func(reply, expected string) rsi.Case {
		return rsi.Case{InstanceID: "mcp-bridge", Contract: contract.Method, Input: json.RawMessage(`{}`), Expected: json.RawMessage(expected), Resources: []rsi.ResourceFixture{
			{Contract: contract.Source, Key: "list:0", Input: json.RawMessage(`{"phase":"list","cursor":""}`), Output: json.RawMessage(`{"next_cursor":"second-page","text":[],"is_error":false}`)},
			{Contract: contract.Source, Key: "list:1", Input: json.RawMessage(`{"phase":"list","cursor":"second-page"}`), Output: json.RawMessage(`{"next_cursor":"","text":[],"is_error":false}`)},
			{Contract: contract.Source, Key: "call", Input: json.RawMessage(`{"phase":"call","cursor":""}`), Output: json.RawMessage(reply)},
		}}
	}
	namespace := func(source, expected string) rsi.Case {
		return rsi.Case{InstanceID: "mcp-bridge", Contract: contract.Namespace, Input: json.RawMessage(`{"input_id":"source"}`), Inputs: []rsi.InputData{{ID: "source", Data: []byte(source)}}, Expected: json.RawMessage(expected)}
	}
	return []rsi.Case{
		method(`{"next_cursor":"","text":["甲","乙"],"is_error":false}`, `{"parts":[{"index":0,"start":0,"end":3},{"index":1,"start":0,"end":3}],"separator":""}`),
		method(`{"next_cursor":"","text":[],"is_error":true}`, `{"parts":[],"separator":""}`),
		namespace("  Original namespace\nSecond line", `{"start":2,"end":20,"truncated":false}`),
		namespace("\t中文目录\n次行", `{"start":1,"end":13,"truncated":false}`),
		namespace(" \n\t ", `{"start":4,"end":4,"truncated":false}`),
		namespace("  "+strings.Repeat("原", 513)+"\nSecond line", `{"start":2,"end":1538,"truncated":true}`),
	}
}
