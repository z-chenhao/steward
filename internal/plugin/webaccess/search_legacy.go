package webaccess

// This is the original web-access-v2 business interpretation for pre-service
// Runs and explicit unbundled/native carriers. Managed calls never use it.
// Retain until those consumers/references are gone and migration is accepted;
// changes to versioned plugin policy must not silently rewrite these Runs.
import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func legacySearchPolicyV2(ctx context.Context, order []string, q SearchQuery, request func(context.Context, string, SearchQuery) (SourceObservation, error)) (SearchResult, error) {
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
			out.Sources = legacyFilterSourcesV2(out.Sources, q.DomainFilter, q.NumResults)
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

func legacyFilterSourcesV2(sources []llm.WebSource, filters []string, limit int) []llm.WebSource {
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

// LegacyFormatSourceV2 is the frozen display recipe used only by ReadSource's explicit historical/native
// consumers. Connectors now return original sanitized facts, with no soft caps.
func LegacyFormatSourceV2(facts SourceObservation) (SourceObservation, error) {
	if facts.Format == "" {
		return facts, nil
	}
	out := facts
	out.Format = ""
	out.Rows = nil
	out.Sources = []llm.WebSource{}
	if facts.Format == "exa" {
		if len(facts.Text) > 20000 {
			return SourceObservation{}, errors.New("OUTPUT_LIMIT")
		}
		for _, row := range facts.Rows {
			if len(out.Sources) >= 20 {
				break
			}
			out.Sources = append(out.Sources, llm.WebSource{URL: row.URL, Title: Preview(row.Title, 1024)})
		}
		return out, nil
	}
	var text strings.Builder
	for _, row := range facts.Rows {
		if len(out.Sources) >= 20 {
			break
		}
		title := Preview(row.Title, 1024)
		out.Sources = append(out.Sources, llm.WebSource{URL: row.URL, Title: title})
		fmt.Fprintf(&text, "%s\n%s\n%s\n\n", title, row.URL, Preview(row.Content, 2048))
	}
	out.Text = Preview(text.String(), 20000)
	return out, nil
}
