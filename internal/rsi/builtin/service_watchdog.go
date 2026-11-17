package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

//go:embed service-watchdog-owner-v1.json
var serviceWatchdogOwner []byte

func serviceWatchdogCases() ([]rsi.Case, error) {
	var c rsi.Case
	if err := json.Unmarshal(serviceWatchdogOwner, &c); err != nil {
		return nil, err
	}
	return []rsi.Case{c}, nil
}
