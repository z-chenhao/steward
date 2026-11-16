// Package contract is a private Root progress contribution, not a Task or model API.
package contract

const PluginID = "steward.task-progress"
const GuidanceContract = "steward.task-progress-guidance/1"
const SelectionContract = "steward.task-progress-selection/1"

type GuidanceFacts struct {
	Role string `json:"role"`
}
type Guidance struct {
	Instructions string `json:"instructions"`
}
type SelectionFacts struct {
	Summary string `json:"summary"`
}
type Selection struct {
	Publish bool `json:"publish"`
}
