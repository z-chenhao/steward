// Package contract is Steward's private exact-PR scan/continuation policy seam.
// It contains no source identifiers, body, URL, credential, Task or SQL model.
package contract

const PluginID = "steward.github-pr-followup"
const Contract = "steward.github-pr-followup/1"
const InputID = "source-eligibility"

type Page struct {
	Kind   string `json:"kind"`
	Number int    `json:"number"`
	Count  int    `json:"count"`
}
type Request struct {
	IncludeIssueComments bool   `json:"include_issue_comments,omitempty"`
	History              []Page `json:"history"`
	PollSeconds          int    `json:"poll_seconds"`
}
type Recipe struct {
	ObservationBytes int    `json:"observation_bytes"`
	GoalBytes        int    `json:"goal_bytes"`
	Notice           string `json:"notice"`
	Method           string `json:"method,omitempty"`
}
type Plan struct {
	Next            *Page  `json:"next"`
	Rows            []int  `json:"rows"`
	DisplayBytes    int    `json:"display_bytes"`
	DisplaySuffix   string `json:"display_suffix"`
	Followup        Recipe `json:"followup"`
	IntervalSeconds int    `json:"interval_seconds"`
	ErrorCode       string `json:"error_code,omitempty"`
}
