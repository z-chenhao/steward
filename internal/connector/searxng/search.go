// Package searxng adapts a configured SearXNG JSON search service.
package searxng

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/z-chenhao/assistant/internal/connector/internal/searchhttp"
	"github.com/z-chenhao/assistant/internal/connector/searchfacts"
	"github.com/z-chenhao/assistant/internal/llm"
)

// Connection binds one service instance to an optional platform SecretStore reference.
type Connection struct {
	URL       string
	SecretRef string
}

func (c Connection) Validate() error {
	u, e := url.Parse(c.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("invalid searxng endpoint")
	}
	return nil
}
func SearchFacts(ctx context.Context, c Connection, query, recency string, secrets llm.SecretStore, client *http.Client) (searchfacts.Result, error) {
	if c.Validate() != nil {
		return searchfacts.Result{}, llm.ModelIncompatible
	}
	u, e := url.Parse(strings.TrimRight(c.URL, "/") + "/search")
	if e != nil {
		return searchfacts.Result{}, llm.ModelIncompatible
	}
	values := u.Query()
	values.Set("q", query)
	values.Set("format", "json")
	if recency != "" {
		values.Set("time_range", recency)
	}
	u.RawQuery = values.Encode()
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return searchfacts.Result{}, llm.ModelIncompatible
	}
	return searchhttp.Read(ctx, request, c.SecretRef, secrets, client)
}
