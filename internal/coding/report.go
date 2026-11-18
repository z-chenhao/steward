package coding

import (
	"encoding/json"
	"time"
)

// Command preserves the accepted action and actual terminal receipt. A failed,
// cancelled or unknown invocation never becomes a successful validation claim.
type Command struct {
	Command    string          `json:"command"`
	AcceptedAt int64           `json:"accepted_at_unix_micro"`
	Attempts   int             `json:"attempts"`
	Error      json.RawMessage `json:"error"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result"`
}

// PRAssociation identifies a previously observed remote head selected by the
// owner for this report. Association alone does not assert content equality;
// Content is present only after the explicit exact-content comparison.
type PRAssociation struct {
	Content      *GitContent `json:"content,omitempty"`
	Kind         string      `json:"kind"`
	Repository   string      `json:"repository"`
	RepositoryID int64       `json:"repository_id"`
	PullNumber   int64       `json:"pull_number"`
	HeadSHA      string      `json:"head_sha"`
	ObservedAt   time.Time   `json:"observed_at"`
}

type Report struct {
	Review *ReviewFacts `json:"review,omitempty"`
	Workspace
	CandidateSHA256   string         `json:"candidate_sha256,omitempty"`
	Checks            []CheckAttempt `json:"checks"`
	ChecksTruncated   bool           `json:"checks_truncated"`
	CommandsTruncated bool           `json:"commands_truncated"`
	Commands          []Command      `json:"commands"`
	Diff              *Diff          `json:"diff,omitempty"`
	PR                *PRAssociation `json:"pr_association,omitempty"`
}
