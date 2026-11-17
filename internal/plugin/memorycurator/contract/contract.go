// Package contract is a private, method-only maintenance seam. No observation,
// memory, source identifier, model credential or mutation crosses it.
package contract

const PluginID = "steward.memory-curator"
const Contract = "steward.memory-curation-policy/1"

type Request struct {
	Style string `json:"style"`
}
type Policy struct {
	Instructions string `json:"instructions"`
}

const ScheduleContract = "steward.memory-dream-schedule/1"

type Schedule struct {
	MinChanges     int `json:"min_changes"`
	MaxWaitSeconds int `json:"max_wait_seconds"`
}
