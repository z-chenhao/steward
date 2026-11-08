package webaccess

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connector/exa"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, code int, media, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestSearchPolicyAndEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result llm.GenerationResult
		err    error
		want   error
	}{
		{name: "zero results are success", result: llm.GenerationResult{WebSearch: &llm.WebSearchResult{Sources: []llm.WebSource{}}}},
		{name: "ordinary prose is not search", result: llm.GenerationResult{Text: "I searched"}, want: llm.InvalidProviderResponse},
		{name: "auth cannot fall back", err: llm.AuthRequired, want: llm.AuthRequired},
		{name: "budget cannot fall back", err: errors.New("BUDGET_DENIED"), want: errors.New("BUDGET_DENIED")},
		{name: "cancel cannot fall back", err: llm.GenerationCancelled, want: llm.GenerationCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r, e := SearchQueryWith(context.Background(), Config{ExaFallback: true}, SearchQuery{Query: "only this query"}, func(_ context.Context, q SearchQuery) (llm.GenerationResult, error) {
				calls++
				if q.Query != "only this query" {
					t.Fatal(q)
				}
				return tc.result, tc.err
			}, nil)
			if calls != 1 || len(r.Attempts) != 1 {
				t.Fatal(calls, r)
			}
			if tc.want == nil {
				if e != nil || r.Provider != "native" || !r.Untrusted || r.Cost == "free" {
					t.Fatal(r, e)
				}
			} else if e == nil || e.Error() != tc.want.Error() {
				t.Fatal(e, tc.want)
			}
		})
	}
	for _, e := range []error{llm.AuthRequired, llm.GenerationCancelled, llm.InvalidProviderResponse, errors.New("BUDGET_APPROVAL_REQUIRED")} {
		if fallbackAllowed(e) {
			t.Fatal(e)
		}
	}
	for _, e := range []error{llm.ModelIncompatible, llm.ProviderUnavailable, llm.RateLimited} {
		if !fallbackAllowed(e) {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := SearchQueryWith(ctx, Config{ExaFallback: true}, SearchQuery{Query: "x"}, nil, nil)
	if !errors.Is(e, llm.GenerationCancelled) {
		t.Fatal(e)
	}
	_, e = SearchQueryWith(context.Background(), Config{}, SearchQuery{Query: " "}, nil, nil)
	if e == nil {
		t.Fatal("empty query")
	}
}

func TestDefinitionsFreezePolicy(t *testing.T) {
	a, b := Config{}, Config{ExaFallback: true}
	if a.Identity() == b.Identity() {
		t.Fatal("policy not frozen")
	}
	defs := Definitions(a)
	if _, e := tool.NewRegistry(defs); e != nil {
		t.Fatal(e)
	}
	if defs[0].Spec.ReplayPolicy != "never" {
		t.Fatal("search can replay paid request")
	}
	if defs[0].Spec.Version != 2 || strings.Contains(defs[0].Spec.Description, "get_search_content") || len(defs[0].PreviousSpecs) != 1 {
		t.Fatal("new search advertises a retired entry point or lost its predecessor")
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	previous := defs[0].PreviousSpecs[0]
	if _, err := registry.Select([]tool.Spec{previous}); err != nil {
		t.Fatal("frozen search v1 no longer resumes", err)
	}
}

func TestLivePublicWebAccess(t *testing.T) {
	if os.Getenv("STEWARD_WEB_ACCESS_LIVE") != "1" {
		t.Skip("explicit live-network opt-in")
	}
	// A public literal IP with a valid TLS certificate also works in fake-IP
	// DNS environments without weakening the production address checks.
	page, e := webcontent.Fetch(context.Background(), "https://1.1.1.1/cdn-cgi/trace")
	if e != nil || !strings.Contains(page.Text, "ip=") {
		t.Fatalf("live public text fetch: %v", e)
	}
	text, sources, e := exa.Search(context.Background(), "site:go.dev Go programming language", 5, nil)
	if e != nil || len(sources) == 0 {
		t.Fatalf("Exa live search sources=%d bytes=%d error=%v", len(sources), len(text), e)
	}
	t.Logf("public text fetch and Exa live search passed (%d sources)", len(sources))
}
