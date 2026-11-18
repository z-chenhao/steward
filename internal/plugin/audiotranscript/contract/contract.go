// Package contract is the private service for formatting one local inference result.
package contract

const (
	PluginID      = "steward.audio-transcript"
	Service       = "steward.audio-transcript/1"
	MaxDurationMS = 10 * 60 * 1000
)

type Request struct {
	Operation  string `json:"operation"`
	DurationMS int64  `json:"duration_ms"`
}
type Segment struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}
type Result struct {
	Decoder  string    `json:"decoder,omitempty"`
	Segments []Segment `json:"segments,omitempty"`
	Error    string    `json:"error,omitempty"`
}
