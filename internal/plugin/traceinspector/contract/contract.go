// Package contract owns the private experimental diagnostic layout seam.
// It contains authorized metadata, never a domain database or model payload.
package contract

const PluginID = "steward.trace-inspector"
const Contract = "steward.trace-layout/1"

type Request struct {
	InputID string `json:"input_id"`
}
type Observation struct {
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
	ID      string `json:"id"`
	Parent  string `json:"parent"`
	Started int64  `json:"started"`
	Ended   *int64 `json:"ended,omitempty"`
}
type Timing struct {
	Attempt string `json:"attempt"`
	Phase   string `json:"phase"`
	At      int64  `json:"at"`
}
type Usage struct {
	KnownUsage   int64 `json:"known_usage"`
	OutputTokens int64 `json:"output_tokens"`
}

type Facts struct {
	SelectedStatus string        `json:"selected_status,omitempty"`
	Usage          *Usage        `json:"usage,omitempty"`
	SelectedID     string        `json:"selected_id,omitempty"`
	Started        int64         `json:"started"`
	Ended          *int64        `json:"ended,omitempty"`
	Now            int64         `json:"now"`
	SelectedType   string        `json:"selected_type"`
	SelectedName   string        `json:"selected_name"`
	Items          []Observation `json:"items"`
	Timings        []Timing      `json:"timings"`
}
type Node struct {
	Primary            bool    `json:"primary,omitempty"`
	PrimaryHasChildren bool    `json:"primary_has_children,omitempty"`
	Running            bool    `json:"running,omitempty"`
	ID                 string  `json:"id"`
	Depth              int     `json:"depth"`
	ParentOutside      bool    `json:"parent_outside"`
	HasChildren        bool    `json:"has_children"`
	Guides             []bool  `json:"guides"`
	X                  float64 `json:"x"`
	Width              float64 `json:"width"`
}
type Timeline struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Layout struct {
	FirstOutputAt *int64    `json:"first_output_at,omitempty"`
	OutputRate    *float64  `json:"output_rate,omitempty"`
	Timeline      *Timeline `json:"timeline,omitempty"`
	Nodes         []Node    `json:"nodes"`
	TTFTMS        *int64    `json:"ttft_ms,omitempty"`
	Tabs          []string  `json:"tabs"`
}
