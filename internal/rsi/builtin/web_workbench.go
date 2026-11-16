package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

// Rebuilt only from independently captured original native sources.
//
//go:embed web-workbench-owner-v1.json
var webWorkbenchOwner []byte

func webWorkbenchCases() ([]rsi.Case, error) {
	var c rsi.Case
	if err := json.Unmarshal(webWorkbenchOwner, &c); err != nil {
		return nil, err
	}
	return []rsi.Case{c}, nil
}
