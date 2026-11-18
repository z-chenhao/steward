package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

// Owner expectation retains the captured v1 structure and the reviewed schedule timezone fields.
//
//go:embed web-workbench-owner-v1.0.1.json
var webWorkbenchOwner []byte

func webWorkbenchCases() ([]rsi.Case, error) {
	var c rsi.Case
	if err := json.Unmarshal(webWorkbenchOwner, &c); err != nil {
		return nil, err
	}
	return []rsi.Case{c}, nil
}
