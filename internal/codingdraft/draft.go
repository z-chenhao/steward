// Package codingdraft publishes one explicitly approved immutable candidate to
// a new branch and a draft pull request in the selected GitHub repository.
package codingdraft

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/interaction"
)

var ErrInvalid = errors.New("invalid coding draft request")
var ErrConflict = errors.New("coding draft selection changed")
var ErrUnknown = errors.New("remote result is unknown; reconcile the original draft")
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,159}$`)
var objectPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidHead(head string) bool { return objectPattern.MatchString(head) }
func ValidDigest(d string) bool  { return digestPattern.MatchString(d) }

type Target struct {
	Repository    string `json:"repository"`
	Base          string `json:"base"`
	Branch        string `json:"branch"`
	Title         string `json:"title"`
	CredentialRef string `json:"credential_ref"`
}

func (t Target) Valid() bool {
	validBranch := func(s string) bool {
		if !branchPattern.MatchString(s) || strings.Contains(s, "..") || strings.Contains(s, "//") || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "/") {
			return false
		}
		for _, part := range strings.Split(s, "/") {
			if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
				return false
			}
		}
		return true
	}
	return repoPattern.MatchString(t.Repository) && !strings.HasSuffix(t.Repository, "/.") && !strings.HasSuffix(t.Repository, "/..") && validBranch(t.Base) && validBranch(t.Branch) && t.Base != t.Branch && strings.TrimSpace(t.Title) != "" && len([]rune(t.Title)) <= 160 && strings.IndexFunc(t.Title, unicode.IsControl) < 0 && interaction.ValidCredentialRef(t.CredentialRef)
}

type Change struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	Mode   string `json:"mode,omitempty"`
}
type Plan struct {
	Sequence     int64    `json:"instruction_sequence"`
	Task         string   `json:"task"`
	Epoch        int64    `json:"task_epoch"`
	Privacy      int64    `json:"privacy_epoch"`
	Target       Target   `json:"target"`
	RepositoryID int64    `json:"repository_id"`
	BaseSHA      string   `json:"base_sha"`
	Candidate    string   `json:"candidate_sha256"`
	Tree         string   `json:"git_tree"`
	CheckCommand string   `json:"check_command"`
	CheckDriver  string   `json:"check_driver"`
	CheckMethod  string   `json:"check_method"`
	CheckScope   string   `json:"check_environment_scope"`
	Proof        string   `json:"verification"`
	VerifiedAt   int64    `json:"verified_at"`
	Graph        string   `json:"graph_revision"`
	Changes      []Change `json:"changes"`
	Message      string   `json:"commit_message"`
	Body         string   `json:"body"`
}
type Receipt struct {
	Approval string `json:"approval_sha256"`
	Status   string `json:"status"`
	Plan     Plan   `json:"plan"`
	Head     string `json:"head,omitempty"`
	Number   int64  `json:"number,omitempty"`
	URL      string `json:"url,omitempty"`
}
type RemoteState struct {
	RepositoryID       int64
	Base, Tree, Branch string
	Entries            []coding.DraftEntry
}
type Pull struct {
	Number          int64
	URL, Head, Base string
}
type Remote interface {
	Inspect(context.Context, Target) (RemoteState, error)
	Blob(context.Context, Target, []byte, string) error
	Tree(context.Context, Target, []coding.DraftEntry, string) error
	Commit(context.Context, Plan) (string, error)
	CreateRef(context.Context, Target, string) error
	CreatePull(context.Context, Plan, string, string) (Pull, error)
	FindPull(context.Context, Plan, string, string) (Pull, bool, error)
}

func Proof(c coding.CheckAttempt) bool {
	if !c.LatestAttempt || c.Status != "succeeded" || c.CandidateMatches == nil || !*c.CandidateMatches {
		return false
	}
	for _, b := range []*bool{c.EnvironmentMatches, c.MethodMatches, c.DriverMatches} {
		if b != nil && !*b {
			return false
		}
	}
	var envelope struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(c.Result, &envelope) != nil {
		return false
	}
	var result struct {
		Outcome   string `json:"outcome"`
		Candidate string `json:"candidate_sha256"`
	}
	return json.Unmarshal([]byte(envelope.Text), &result) == nil && result.Outcome == "passed" && result.Candidate == c.CandidateSHA256
}
func Date(at int64) string {
	return time.UnixMicro(at).UTC().Truncate(time.Second).Format(time.RFC3339)
}
