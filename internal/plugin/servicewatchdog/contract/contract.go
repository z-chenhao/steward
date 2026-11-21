// Package contract is the private, data-free restart recipe seam.
package contract

const PluginID = "steward.service-watchdog"
const Service = "steward.service-watchdog/1"

type Recipe struct {
	AlertAfterSeconds uint16 `json:"alert_after_seconds,omitempty"`
	Attempts          uint8  `json:"attempts"`
	RetryDelayMS      uint32 `json:"retry_delay_ms"`
}
