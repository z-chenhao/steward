// Package contract is a private shell execution recipe. The implementation
// receives no command, workspace, execution identity or credentials.
package contract

const PluginID = "steward.shell-tools"
const Contract = "steward.shell-limits/1"

type Ceilings struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	OutputBytes    int `json:"output_bytes"`
}

type Facts struct {
	Ceilings         Ceilings `json:"ceilings"`
	RequestedTimeout *int     `json:"requested_timeout,omitempty"`
}

type Limits struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	OutputBytes    int `json:"output_bytes"`
}
