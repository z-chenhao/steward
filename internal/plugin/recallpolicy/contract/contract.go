// Package contract defines the private authorized-evidence selection seam.
package contract

const PluginID = "steward.recall"
const Contract = "steward.recall-selection/1"
const QueryContract = "steward.recall-query/1"

type Request struct {
	InputID string `json:"input_id"`
}
type Candidate struct {
	Content    string   `json:"content"`
	Layer      string   `json:"layer"`
	SourceType string   `json:"source_type"`
	Relevance  float64  `json:"relevance"`
	Score      float64  `json:"score"`
	Quotes     []string `json:"quotes"`
}
type Facts struct {
	Query         string      `json:"query"`
	EvidenceFirst bool        `json:"evidence_first"`
	ExcerptBytes  int         `json:"excerpt_bytes"`
	Candidates    []Candidate `json:"candidates"`
}
type Selection struct {
	Index    int   `json:"index"`
	Offset   int   `json:"offset"`
	Length   int   `json:"length"`
	Evidence []int `json:"evidence"`
}
type Plan struct {
	Entries []Selection `json:"entries"`
}
type QueryFacts struct {
	Latest   string `json:"latest"`
	Previous string `json:"previous"`
	Answer   string `json:"answer"`
}
type QueryPlan struct {
	Query string `json:"query"`
}
