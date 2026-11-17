// Package contract is a private Task discovery/label seam. No Task identifiers,
// control receipts, approval authority or mutable state cross it.
package contract

const PluginID = "steward.task-control"
const SearchContract = "steward.task-discovery/1"
const LabelContract = "steward.task-label/1"

type Request struct {
	InputID string `json:"input_id"`
}
type Candidate struct {
	Text      string `json:"text"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}
type SearchFacts struct {
	Query      string      `json:"query"`
	Limit      int         `json:"limit"`
	Candidates []Candidate `json:"candidates"`
}
type SearchPlan struct {
	Indices []int `json:"indices"`
}
type LabelFacts struct {
	Text     string `json:"text"`
	Explicit bool   `json:"explicit"`
}
type LabelPlan struct {
	Title string `json:"title"`
}
