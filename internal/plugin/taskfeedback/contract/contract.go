// Package contract is a private feedback recipe. No question, answer, Task or
// artifact identity crosses this seam.
package contract

const PluginID = "steward.task-feedback"
const Contract = "steward.task-feedback-policy/1"

type Facts struct {
	Kind string `json:"kind"`
}
type Policy struct {
	SeparateKinds    bool   `json:"separate_kinds,omitempty"`
	LifetimeSeconds  int64  `json:"lifetime_seconds"`
	ResponseGuidance string `json:"response_guidance"`
}
