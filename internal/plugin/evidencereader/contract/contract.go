// Package contract defines a private paging policy. Original evidence, object
// identities, permissions and cursor construction remain inside Host.
package contract

const PluginID = "steward.evidence-reader"
const TextContract = "steward.evidence-text-window/1"
const LinksContract = "steward.evidence-link-window/1"

type TextFacts struct {
	Bytes      int   `json:"bytes"`
	Boundaries []int `json:"boundaries"`
	Breaks     []int `json:"breaks"`
}
type TextPlan struct {
	Bytes int `json:"bytes"`
}
type LinkFacts struct {
	Candidates int `json:"candidates"`
}
type LinkPlan struct {
	Take int `json:"take"`
}
