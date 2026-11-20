package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

// Original HTTP/preview captures and independent literal workflow expectations
// belong to the owner. They never read or execute a candidate to build answers.
//
//go:embed github-cases.json
var githubAcceptance []byte

//go:embed github-issue-comment-cases.json
var githubIssueCommentsAcceptance []byte

func githubCases(includeIssueComments bool) ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(githubAcceptance, &cases)
	if err != nil || !includeIssueComments {
		return cases, err
	}
	var comments []rsi.Case
	if err := json.Unmarshal(githubIssueCommentsAcceptance, &comments); err != nil {
		return nil, err
	}
	return append(cases, comments...), nil
}
