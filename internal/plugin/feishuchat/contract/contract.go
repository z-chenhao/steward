// Package contract is a repository-private experimental Feishu presentation seam.
// Credential state, expiry, authentication and transport stay authoritative Host facts.
package contract

const PluginID = "steward.feishu-chat"
const Status = "steward.feishu-status/1"

type StatusFacts struct {
	State string `json:"state"`
}
type StatusProjection struct {
	Action string `json:"action"`
}

const Card = "steward.feishu-card/1"

// CardFacts contains presentation data, never credentials or transport authority.
type CardFacts struct {
	Fallback   string          `json:"fallback"`
	Components []CardComponent `json:"components"`
	Linked     bool            `json:"linked"`
	Feedback   bool            `json:"feedback"`
	Review     bool            `json:"review"`
}
type CardComponent struct {
	Valid      bool        `json:"valid"`
	Kind       string      `json:"kind"`
	Text       string      `json:"text"`
	Columns    []string    `json:"columns"`
	Rows       [][]string  `json:"rows"`
	ApprovalID string      `json:"approval_id"`
	Hash       string      `json:"hash"`
	Fields     []CardField `json:"fields"`
}
type CardField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type FeedbackCopy struct {
	Invalid string `json:"invalid"`
	Empty   string `json:"empty"`
	Saved   string `json:"saved"`
	Stale   string `json:"stale"`
	Unknown string `json:"unknown"`
}
type CardProjection struct {
	FeedbackCopy *FeedbackCopy `json:"feedback_copy,omitempty"`
	Mode         string        `json:"mode"`
	Text         string        `json:"text"`
	LinkLabel    string        `json:"link_label"`
	Placeholder  string        `json:"placeholder"`
	SubmitLabel  string        `json:"submit_label"`
}
