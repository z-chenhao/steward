// Package contract is private CodeMode policy and documentation formatting.
// It carries no script, execution identity, Tool authority or runtime handle.
package contract

const PluginID = "steward.codemode"
const LimitsContract = "steward.codemode-limits/1"
const FormatContract = "steward.codemode-input-type/1"

type Limits struct {
	TimeoutMS       int `json:"timeout_ms"`
	MemoryBytes     int `json:"memory_bytes"`
	OutputBytes     int `json:"output_bytes"`
	PendingCalls    int `json:"pending_calls"`
	InterruptBudget int `json:"interrupt_budget"`
}
type LimitsFacts struct {
	Role     string `json:"role"`
	Ceilings Limits `json:"ceilings"`
}
type LimitsPlan struct {
	Limits Limits `json:"limits"`
}
type Request struct {
	InputID string `json:"input_id"`
}
type FormatPlan struct {
	InputType string `json:"input_type"`
	Guidance  string `json:"guidance"`
}
