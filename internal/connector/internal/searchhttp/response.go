// Package searchhttp holds the shared JSON result transport of the two search APIs.
package searchhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/searchfacts"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func Read(ctx context.Context, request *http.Request, ref string, secrets llm.SecretStore, client *http.Client) (searchfacts.Result, error) {
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	request.Header.Set("Accept", "application/json")
	if request.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	secret := ""
	if ref != "" {
		if secrets == nil {
			return searchfacts.Result{}, llm.AuthRequired
		}
		value, e := secrets.Get(ctx, ref)
		if e != nil || len(value) == 0 || len(value) > 16384 || strings.ContainsAny(string(value), "\r\n\x00") {
			return searchfacts.Result{}, llm.AuthRequired
		}
		secret = string(value)
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return searchfacts.Result{}, llm.GenerationCancelled
		}
		return searchfacts.Result{}, llm.ProviderUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return searchfacts.Result{}, llm.AuthRequired
	}
	if response.StatusCode == 429 {
		return searchfacts.Result{}, llm.RateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return searchfacts.Result{}, llm.ProviderUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if e != nil || len(raw) > 256<<10 {
		return searchfacts.Result{}, llm.InvalidProviderResponse
	}
	var result struct {
		Results *[]struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Results == nil {
		return searchfacts.Result{}, llm.InvalidProviderResponse
	}
	rows := []searchfacts.Row{}
	redact := func(value string) string {
		if secret == "" {
			return value
		}
		seen := map[string]bool{}
		pairs := []string{}
		for _, token := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			if !seen[token] {
				seen[token] = true
				pairs = append(pairs, token, "[redacted]")
			}
		}
		return strings.NewReplacer(pairs...).Replace(value)
	}

	for _, entry := range *result.Results {
		if webcontent.ValidURL(entry.URL) != nil || secret != "" && (strings.Contains(entry.URL, secret) || strings.Contains(entry.URL, url.QueryEscape(secret)) || strings.Contains(entry.URL, url.PathEscape(secret))) {
			continue
		}
		rows = append(rows, searchfacts.Row{URL: entry.URL, Title: redact(entry.Title), Content: redact(entry.Content)})
	}
	return searchfacts.Result{Format: "json", Rows: rows}, nil
}
