// Package contract is a private delegation method under frozen Host ceilings.
// Task text, capabilities, model identities and ancestry are never policy output.
package contract

const PluginID = "steward.delegation"
const Contract = "steward.delegation-policy/1"

type Ceilings struct {
	Depth       int `json:"depth"`
	Descendants int `json:"descendants"`
}
type Policy struct {
	Depth        int    `json:"depth"`
	Descendants  int    `json:"descendants"`
	Instructions string `json:"instructions"`
}
