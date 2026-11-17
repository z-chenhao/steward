package exa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, code int, media, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestExaMCPContract(t *testing.T) {
	methods := []string{}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != exaEndpoint || r.Header.Get("Authorization") != "" {
			t.Fatal("unapproved destination or credential")
		}
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Fatal("request")
		}
		methods = append(methods, request.Method)
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26"}
		case "notifications/initialized":
			return response(r, 202, "application/json", ""), nil
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "web_search_exa", "inputSchema": map[string]any{"properties": map[string]any{"query": map[string]string{"type": "string"}}}}}}
		case "tools/call":
			var params struct {
				Arguments struct {
					Query      string `json:"query"`
					NumResults int    `json:"numResults"`
				} `json:"arguments"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.Arguments.NumResults != 2 || params.Arguments.Query != "only this query" {
				t.Fatal(string(request.Params))
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Title: Example\nURL: https://example.com/\nText: Evidence"}}}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		return response(r, 200, "text/event-stream", "event: message\ndata: "+string(raw)+"\n\n"), nil
	})}
	facts, e := SearchFacts(context.Background(), "only this query", 2, client)
	if e != nil || !strings.Contains(facts.Text, "Evidence") || len(facts.Rows) != 1 || len(methods) != 4 {
		t.Fatal(facts, methods, e)
	}
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 429, "application/json", ""), nil })
	_, e = SearchFacts(context.Background(), "x", 5, client)
	if !errors.Is(e, llm.RateLimited) {
		t.Fatal(e)
	}
}
