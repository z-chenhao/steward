package contract

const Demonstration = "steward.browser-demonstration/1"

// A control address contains the semantic label the operator chose to keep.
// Page URLs, field values, DOM, browser storage and credentials are outside this protocol.
type Step struct {
	Action string `json:"action"`
	Label  string `json:"label"`
}
type Transition struct {
	From int  `json:"from"`
	Step Step `json:"step"`
	To   int  `json:"to"`
}
type DemoRequest struct {
	Name   string       `json:"name"`
	Steps  []Step       `json:"steps"`
	Sample []Transition `json:"sample"`
	Goal   int          `json:"goal"`
}
type DemoResult struct {
	Steps    []string `json:"steps"`
	Matched  int      `json:"matched"`
	Verified bool     `json:"verified"`
}
