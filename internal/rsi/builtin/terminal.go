package builtin

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
)

//go:embed terminal-owner-v1.json
var terminalOwner []byte

func terminalCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(terminalOwner, &cases)
	return cases, err
}
