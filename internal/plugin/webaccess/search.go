package webaccess

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/exa"
	"github.com/z-chenhao/assistant/internal/connector/searchfacts"
	"github.com/z-chenhao/assistant/internal/connector/searxng"
	"github.com/z-chenhao/assistant/internal/connector/tavily"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

var errFiltersUnsupported = errors.New("WEB_FILTERS_UNSUPPORTED")

type SearchQuery struct {
	Query         string   `json:"query"`
	Provider      string   `json:"provider,omitempty"`
	NumResults    int      `json:"numResults,omitempty"`
	DomainFilter  []string `json:"domainFilter,omitempty"`
	RecencyFilter string   `json:"recencyFilter,omitempty"`
}
type QuerySearch func(context.Context, SearchQuery) (llm.GenerationResult, error)

func (q SearchQuery) Validate() error {
	if strings.TrimSpace(q.Query) == "" || len(q.Query) > 4096 || q.NumResults < 0 || q.NumResults > 20 || len(q.DomainFilter) > 20 {
		return errors.New("INVALID_ARGUMENT")
	}
	if q.RecencyFilter != "" && q.RecencyFilter != "day" && q.RecencyFilter != "week" && q.RecencyFilter != "month" && q.RecencyFilter != "year" {
		return errors.New("INVALID_ARGUMENT")
	}
	for _, filter := range q.DomainFilter {
		d := strings.TrimPrefix(filter, "-")
		u, e := url.Parse("https://" + d)
		if e != nil || d == "" || len(d) > 253 || u.Hostname() != d || u.User != nil || u.Port() != "" || strings.ContainsAny(d, "/\\?#@% ") {
			return errors.New("INVALID_ARGUMENT")
		}
	}
	return nil
}
func SearchQueryWith(ctx context.Context, c Config, q SearchQuery, hosted QuerySearch, secrets llm.SecretStore) (SearchResult, error) {
	return searchQueryWith(ctx, c, q, hosted, secrets, nil)
}
func searchQueryWith(ctx context.Context, c Config, q SearchQuery, hosted QuerySearch, secrets llm.SecretStore, client *http.Client) (SearchResult, error) {
	return LegacySearch(ctx, c, q, func(ctx context.Context, source string, q SearchQuery) (SourceObservation, error) {
		return ReadSource(ctx, c, source, q, hosted, secrets, client)
	})
}

// LegacySearch is the explicit native/pre-service business boundary. Managed
// searches execute this policy in their frozen plugin, never as a fallback.
func LegacySearch(ctx context.Context, c Config, q SearchQuery, request func(context.Context, string, SearchQuery) (SourceObservation, error)) (SearchResult, error) {
	if c.Validate() != nil {
		out, err := legacySearchPolicyV2(ctx, nil, q, nil)
		if err != nil && err.Error() == "INVALID_ARGUMENT" {
			return out, err
		}
		return out, llm.ModelIncompatible
	}
	return legacySearchPolicyV2(ctx, c.Order(), q, request)
}

// SourceObservation contains provider facts, not a research/routing decision.
// Configuration, credential references and model state never cross this seam.
type SourceObservation struct {
	Format      string
	Rows        []SourceRow
	Text        string
	Sources     []llm.WebSource
	Usage       *llm.TokenUsage
	RetrievedAt string
}

type SourceRow = searchfacts.Row

// ReadSource retains the original pre-normalizer interpretation for explicit
// native/pre-format-service consumers. Managed format calls use ReadSourceFacts.
// Remove when no retained Run/reference/unbundled carrier needs V2 formatting.
func ReadSource(ctx context.Context, c Config, source string, q SearchQuery, hosted QuerySearch, secrets llm.SecretStore, client *http.Client) (SourceObservation, error) {
	facts, err := ReadSourceFacts(ctx, c, source, q, hosted, secrets, client)
	if err != nil {
		return facts, err
	}
	return LegacyFormatSourceV2(facts)
}

// ReadSourceFacts is a single fixed provider operation. The caller owns the accepted
// query, configured provider/endpoint, model receipt, budget and original replay.
// It makes no default, fallback, ranking, soft display limit or research decision.
func ReadSourceFacts(ctx context.Context, c Config, source string, q SearchQuery, hosted QuerySearch, secrets llm.SecretStore, client *http.Client) (SourceObservation, error) {
	var observed SourceObservation
	if q.Validate() != nil || q.NumResults < 1 {
		return observed, errors.New("INVALID_ARGUMENT")
	}
	if c.Validate() != nil {
		return observed, llm.ModelIncompatible
	}
	allowed := false
	for _, candidate := range c.Order() {
		allowed = allowed || candidate == source
	}
	if !allowed {
		return observed, errors.New("PERMISSION_DENIED")
	}
	var err error
	var facts searchfacts.Result
	switch source {
	case "native":
		if hosted == nil {
			err = llm.ModelIncompatible
			break
		}
		var result llm.GenerationResult
		result, err = hosted(ctx, q)
		if err == nil && (result.WebSearch == nil || result.Clipped || len(result.ToolCalls) > 0) {
			err = llm.InvalidProviderResponse
		}
		if err == nil {
			observed.Text = result.Text
			observed.Sources = result.WebSearch.Sources
			observed.Usage = result.Usage
		}
	case "exa":
		if len(q.DomainFilter) > 0 || q.RecencyFilter != "" {
			err = errFiltersUnsupported
			break
		} // No pretending query hints are strict Exa filters.
		facts, err = exa.SearchFacts(ctx, q.Query, q.NumResults, client)
	case "searxng":
		facts, err = searxng.SearchFacts(ctx, searxng.Connection{URL: c.SearXNGURL, SecretRef: c.SearXNGSecretRef}, q.Query, q.RecencyFilter, secrets, client)
	case "tavily":
		request := tavily.Request{Query: q.Query, NumResults: q.NumResults, TimeRange: q.RecencyFilter}
		for _, domain := range q.DomainFilter {
			if strings.HasPrefix(domain, "-") {
				request.ExcludeDomains = append(request.ExcludeDomains, domain[1:])
			} else {
				request.IncludeDomains = append(request.IncludeDomains, domain)
			}
		}
		facts, err = tavily.SearchFacts(ctx, tavily.Connection{SecretRef: c.TavilySecretRef}, request, secrets, client)
	}
	if source != "native" {
		observed.Format, observed.Text, observed.Rows = facts.Format, facts.Text, facts.Rows
	}
	if err == nil {
		observed.RetrievedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return observed, err
}

// searchQueryPolicy owns the current source selection, absent result count,
// fallback and source presentation. Its resource function is supplied either by
// the isolated plugin transport or the explicit historical/native caller.
func searchQueryPolicy(ctx context.Context, order []string, q SearchQuery, request func(context.Context, string, SearchQuery) (SourceObservation, error)) (SearchResult, error) {
	out := SearchResultBase(q.Query)
	if q.Validate() != nil {
		return out, errors.New("INVALID_ARGUMENT")
	}
	if q.NumResults == 0 {
		q.NumResults = 5
	}
	if q.Provider != "" && q.Provider != "auto" {
		allowed := false
		for _, source := range order {
			if source == q.Provider {
				allowed = true
			}
		}
		if !allowed {
			return out, errors.New("PERMISSION_DENIED")
		}
		order = []string{q.Provider}
	}
	var err error = llm.ModelIncompatible
	for _, source := range order {
		if ctx.Err() != nil {
			return out, llm.GenerationCancelled
		}
		var observed SourceObservation
		observed, err = request(ctx, source, q)
		if err == nil {
			out.Text, out.Sources, out.Usage = observed.Text, observed.Sources, observed.Usage
		}
		outcome := "completed"
		if err != nil {
			outcome = SourceAttemptCode(err)
		}
		out.Attempts = append(out.Attempts, Attempt{source, outcome})
		if err == nil {
			out.Provider = source
			out.Sources = filterSources(out.Sources, q.DomainFilter, q.NumResults)
			if len(q.DomainFilter) > 0 {
				out.Text = "Source URLs filtered locally; consult originals for evidence.\n"
				for _, source := range out.Sources {
					out.Text += source.Title + "\n" + source.URL + "\n"
				}
			}
			out.RetrievedAt = observed.RetrievedAt
			return out, nil
		}
		if !SourceFallbackAllowed(err) {
			return out, err
		}
	}
	return out, err
}

func filterSources(sources []llm.WebSource, filters []string, limit int) []llm.WebSource {
	out := []llm.WebSource{}
	seen := map[string]bool{}
	for _, source := range sources {
		if webcontent.ValidURL(source.URL) != nil || seen[source.URL] {
			continue
		}
		u, _ := url.Parse(source.URL)
		host := strings.ToLower(u.Hostname())
		hasInclude, include, exclude := false, false, false
		for _, filter := range filters {
			deny := strings.HasPrefix(filter, "-")
			domain := strings.ToLower(strings.TrimPrefix(filter, "-"))
			match := host == domain || strings.HasSuffix(host, "."+domain)
			if deny {
				exclude = exclude || match
			} else {
				hasInclude = true
				include = include || match
			}
		}
		if exclude || hasInclude && !include {
			continue
		}
		seen[source.URL] = true
		source.Title = webcontent.Preview(source.Title, 1024)
		out = append(out, source)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// SearchResultBase supplies mandatory provenance/billing facts. It contains no
// default routing, result selection, filtering or workflow policy.
func SearchResultBase(query string) SearchResult {
	return SearchResult{Query: query, Sources: []llm.WebSource{}, Attempts: []Attempt{}, Untrusted: true, Freshness: "provider_managed_unknown", Cost: "provider controlled; external search fees are outside the model-token budget"}
}
