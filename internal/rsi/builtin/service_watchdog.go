package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

//go:embed service-watchdog-owner-v1.json
var serviceWatchdogOwner []byte

func serviceWatchdogCases(alerts bool) ([]rsi.Case, error) {
	var c rsi.Case
	if err := json.Unmarshal(serviceWatchdogOwner, &c); err != nil {
		return nil, err
	}
	if alerts {
		c.Expected = json.RawMessage(`{"attempts":3,"retry_delay_ms":60000,"alert_after_seconds":60}`)
	}
	return []rsi.Case{c}, nil
}
