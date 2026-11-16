package contract

const DiffContract = "steward.trace-diff/1"

// Record contains identities and an exact stored-value fingerprint. Comparing
// context does not require releasing prompt, tool or private Memory bodies.
type Record struct {
	Index      int     `json:"index"`
	Label      string  `json:"label"`
	SourceRef  string  `json:"source_ref,omitempty"`
	CallID     string  `json:"call_id,omitempty"`
	ToolName   *string `json:"tool_name,omitempty"`
	ValueKnown bool    `json:"value_known"`
	Digest     string  `json:"digest"`
}
type DiffFacts struct {
	Section      string   `json:"section"`
	After        int      `json:"after"`
	Before       []Record `json:"before"`
	AfterRecords []Record `json:"after_records"`
}
type Change struct {
	Kind   string `json:"kind"`
	Before *int   `json:"before,omitempty"`
	After  *int   `json:"after,omitempty"`
	Label  string `json:"label"`
}
type Diff struct {
	Items []Change `json:"items"`
	Next  *int     `json:"next,omitempty"`
}
