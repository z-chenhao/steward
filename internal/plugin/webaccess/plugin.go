package webaccess

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

// Host is the plugin's private consumer contract. Its platform implementation
// admits model calls and resolves scoped evidence; it never gives the plugin a DB.
type Host interface {
	Generate(context.Context, string, ModelCall) (llm.GenerationResult, error)
	ReadEvidence(context.Context, string) ([]byte, error)
}

const MaxEvidenceBytes = 24 << 20

type ModelCall struct {
	Kind     string
	Query    *SearchQuery
	Messages []llm.Message
}

// Plugin composes the existing tools. Source selection and research recipes are
// policy, independent of the host's admission, ledger and atomic publication.
var ErrSearchMethod = errors.New("WEB_SEARCH_METHOD_FAILED")

type Plugin struct {
	Search        func(context.Context, int, SearchQuery) (SearchResult, error)
	Config        Config
	NativeSearch  bool
	ContextWindow int64
	Secrets       llm.SecretStore
	FetchContent  func(context.Context, webcontent.Config, string, string) (webcontent.Page, error)
	Fetch         func(context.Context, string) (webcontent.Page, error)
}
type researchInput struct {
	SearchQuery
	Queries        []string `json:"queries,omitempty"`
	URL            string   `json:"url"`
	URLs           []string `json:"urls,omitempty"`
	Mode           string   `json:"mode"`
	Prompt         string   `json:"prompt"`
	Workflow       string   `json:"workflow"`
	IncludeContent bool     `json:"includeContent"`
	Claim          string   `json:"claim"`
}
type fetchError struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}
type researchEnvelope struct {
	ResponseID string `json:"responseId,omitempty"`
	Document
	Summary         string       `json:"summary,omitempty"`
	ReviewRequired  bool         `json:"reviewRequired,omitempty"`
	Errors          []fetchError `json:"errors,omitempty"`
	HasMore         bool         `json:"has_more"`
	Untrusted       bool         `json:"untrusted"`
	FilterGuarantee string       `json:"filter_guarantee"`
	Cost            string       `json:"cost"`
}
