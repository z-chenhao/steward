// Package contract is the private selection service for meeting preparation.
// Source links, message bodies and account identity stay with Host.
package contract

const PluginID = "steward.meeting-preparation"
const Select = "steward.meeting-preparation/1"

type Input struct {
	Meetings []string `json:"meetings"`
	Subjects []string `json:"subjects"`
}
type Pair struct {
	Meeting int `json:"meeting"`
	Thread  int `json:"thread"`
}
