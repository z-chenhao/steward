package builtin

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
)

//go:embed feishu-status-owner-v1.json
var feishuOwner []byte

func feishuCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(feishuOwner, &cases)
	return cases, err
}
