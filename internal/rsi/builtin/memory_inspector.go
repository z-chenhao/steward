package builtin

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Captured original navigation/view/style/copy and independently reviewed Worker
// bytes. These are Owner acceptance expectations, never candidate-supplied policy.
// Worker semantics additionally require the captured original client regressions.
//
//go:embed memory-inspector-owner-v1.json
var memoryInspectorAcceptance []byte

func memoryInspectorCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(memoryInspectorAcceptance, &cases)
	return cases, err
}
