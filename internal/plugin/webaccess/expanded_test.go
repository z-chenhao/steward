package webaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/llm"
)

func TestWebAccessContentSpansUnicodeAndAssessment(t *testing.T) {
	text := "前言 İSTANBUL 中文证据：Fact one\nFact two。" + strings.Repeat("尾 ", 5000)
	doc := Document{Version: 1, Pages: []Page{{URL: "https://example.com", Text: text}}}
	for _, tc := range []struct{ pattern, mode, expected string }{{"istanbul", "case-insensitive", "İSTANBUL"}, {"Fact one Fact two", "fuzzy", "Fact one\nFact two"}, {"中文证据", "exact", "中文证据"}} {
		start, end, e := FindSpanV1(text, tc.pattern, tc.mode)
		if e != nil || start < 0 || text[start:end] != tc.expected {
			t.Fatal(tc, start, end, e)
		}
		found, e := ReadContent(doc, 0, 0, 0, 400, tc.pattern, tc.mode)
		if e != nil || found.Found == nil || !*found.Found || !utf8.ValidString(found.Text) {
			t.Fatal(found, e)
		}
	}
	for _, offset := range []int{1, 2, 3, len(text)} {
		slice, e := ReadContent(doc, 0, 0, offset, 30, "", "")
		if e != nil || !utf8.ValidString(slice.Text) {
			t.Fatal(slice, e)
		}
	}
	doc.Claim = "Does evidence prove the claim?"
	doc.Assess()
	if doc.Status != "unclear" || len(doc.Passages) != 1 || doc.Passages[0].Text != text[doc.Passages[0].Start:doc.Passages[0].End] || doc.Passages[0].ContentHash != Hash(doc.Passages[0].Text) {
		t.Fatal(doc.Passages)
	}
}

type searchSecrets struct{}

func (searchSecrets) Get(context.Context, string) ([]byte, error) {
	return []byte("synthetic-search-secret"), nil
}
func (searchSecrets) Put(context.Context, string, []byte) error { return nil }
func (searchSecrets) Delete(context.Context, string) error      { return nil }
func TestWebAccessConfiguredSourcesAndTerminalFailures(t *testing.T) {
	for _, source := range []string{"searxng", "tavily"} {
		t.Run(source, func(t *testing.T) {
			calls := 0
			c := Config{SourceOrder: []string{source, "native"}, SearXNGURL: "https://selected-search.example", SearXNGSecretRef: "secret-ref", TavilySecretRef: "secret-ref"}
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer synthetic-search-secret" {
					t.Error("credential missing")
				}
				if source == "searxng" && (r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "selected-search.example" || r.URL.Path != "/search" || r.URL.Query().Get("q") != "public query" || r.URL.Query().Get("time_range") != "week") {
					t.Error(r.URL)
				}
				if source == "tavily" {
					if r.Method != http.MethodPost || r.URL.String() != "https://api.tavily.com/search" {
						t.Fatal("unapproved Tavily destination")
					}
					var request map[string]any
					if json.NewDecoder(r.Body).Decode(&request) != nil || request["query"] != "public query" || request["time_range"] != "week" || request["max_results"] != float64(5) || request["search_depth"] != "basic" || request["include_answer"] != false || request["include_raw_content"] != false {
						t.Error(request)
					}
				}
				return response(r, 200, "application/json", `{"results":[{"url":"https://docs.example.com/facts","title":"Official","content":"synthetic-search-secret must not persist"}]}`), nil
			})}
			result, e := searchQueryWith(context.Background(), c, SearchQuery{Query: "public query", RecencyFilter: "week", DomainFilter: []string{"example.com"}}, func(context.Context, SearchQuery) (llm.GenerationResult, error) {
				t.Fatal("unexpected fallback")
				return llm.GenerationResult{}, nil
			}, searchSecrets{}, client)
			if e != nil || calls != 1 || len(result.Sources) != 1 || strings.Contains(result.Text, "synthetic-search-secret") {
				t.Fatal(result, e, calls)
			}
			for _, status := range []int{401, 429, 200} {
				client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
					if status == 200 {
						return response(r, 200, "application/json", `{}`), nil
					}
					return response(r, status, "application/json", `{}`), nil
				})
				fallback := 0
				_, e = searchQueryWith(context.Background(), c, SearchQuery{Query: "q"}, func(context.Context, SearchQuery) (llm.GenerationResult, error) {
					fallback++
					return llm.GenerationResult{WebSearch: &llm.WebSearchResult{}}, nil
				}, searchSecrets{}, client)
				if status == 429 {
					if e != nil || fallback != 1 {
						t.Fatal(status, e, fallback)
					}
				} else if e == nil || fallback != 0 {
					t.Fatal(status, e, fallback)
				}
			}
		})
	}
	c := Config{SourceOrder: []string{"native"}}
	_, e := SearchQueryWith(context.Background(), c, SearchQuery{Query: "private purpose", Provider: "exa"}, nil, nil)
	if e == nil {
		t.Fatal("disabled provider accepted")
	}
	_, e = SearchQueryWith(context.Background(), Config{SourceOrder: []string{"exa"}}, SearchQuery{Query: "q", DomainFilter: []string{"example.com"}}, nil, nil)
	if e != errFiltersUnsupported {
		t.Fatal("unsupported Exa filters silently degraded", e)
	}
}
func TestWebAccessHasNoAgentSDKDependency(t *testing.T) {
	raw, e := os.ReadFile("../../../runtime/javascript/package.json")
	if e != nil {
		t.Fatal(e)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		t.Fatal("package manifest")
	}
	allowed := map[string]bool{"quickjs-wasi": true, "@mozilla/readability": true, "defuddle": true, "linkedom": true, "turndown": true, "unpdf": true}
	for dependency := range pkg.Dependencies {
		if !allowed[dependency] {
			t.Fatalf("unexpected runtime dependency %s; review agent/SDK boundary", dependency)
		}
	}
	if _, e = os.Stat("../../../runtime/javascript/src/vendor/web-access"); e == nil {
		t.Fatal("external agent implementation must not be vendored")
	}
}
