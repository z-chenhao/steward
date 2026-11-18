package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestWebPluginFetchActualUpgradeOriginalRunRollbackAndSafeReplay(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil)
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:3]
	policy.Cases = policy.Cases[:4]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.1"))
	old, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("原文🧭 ", 2000) + "original end"
	cut := func(limit int) int {
		for !utf8.ValidString(full[:limit]) {
			limit--
		}
		return limit
	}
	fetches := 0
	r.webFetch = func(_ context.Context, url string) (webaccess.Page, error) {
		fetches++
		if url != "https://example.com/original" {
			t.Fatal("rewritten URL", url)
		}
		return webaccess.Page{URL: url, Title: "original title", Text: full, RetrievedAt: "2026-11-09T00:00:00Z", Freshness: "origin may cache", Untrusted: true}, nil
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "source observed"}, nil
		}
		return calls(webaccess.FetchName, `{"url":"https://example.com/original"}`), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Read the explicit public page").RunID }
	check := func(id string, want int, managed bool) {
		t.Helper()
		before := fetches
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.FetchName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var value struct {
			webaccess.Page
			Ref   string `json:"artifact_ref"`
			Bytes int    `json:"total_bytes"`
			Next  int    `json:"next_offset"`
			More  bool   `json:"has_more"`
		}
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || value.Ref == "" || value.Text != full[:want] || value.Next != want || value.Bytes != len(full) || !value.More || value.Title != "original title" || value.RetrievedAt != "2026-11-09T00:00:00Z" || fetches != before+1 {
			t.Fatal(raw, fetches, before)
		}
		if readArtifact(t, s, toolRunScope(t, s, id), value.Ref) != full {
			t.Fatal("full page replaced by preview")
		}
		if flag := scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) c WHERE run_id=? AND c.value=?`, id, webaccess.FetchContract); managed != (flag == 1) {
			t.Fatal(flag, managed)
		}
	}
	native := queue("fetch-original-three-service-run")
	first, _ := selectWebVersion(t, h, old.Revision, "1.0.0", 5, 0)
	check(native, cut(4096), false)
	queued := queue("fetch-original-full-run")
	second, pkg := selectWebVersion(t, h, first, "1.13.0", 5, 0)
	check(queued, cut(4096), true)
	check(queue("fetch-short-preview"), cut(1024), true)
	activateDelegation(t, h, "fetch-rollback", second, first, "web-search-owner-1.0.0")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(queue("fetch-restored-preview"), cut(4096), true)
	if _, err = h.Store.Deactivate(ctx, "fetch-disable", first); err != nil {
		t.Fatal(err)
	}
	before := fetches
	disabled := queue("fetch-disabled")
	mustDrain(t, a)
	if fetches != before || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='failed' AND json_extract(error_json,'$.code')='PROVIDER_UNAVAILABLE'`, disabled, webaccess.FetchName) != 1 {
		t.Fatal("deactivation resurrected native fetch", fetches, before)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("fetch added another effect ledger")
	}
}

func TestWebPluginFetchAcceptedForgedRangeCannotInventPreview(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "forged-fetch", Name: webaccess.FetchName, Arguments: `{"url":"https://example.com/original"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		initial, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectWebVersion(t, h, initial.Revision, "1.14.0", 5, 0)
	})
	defer r.Release(ctx, claim)
	fetches := 0
	r.webFetch = func(context.Context, string) (webaccess.Page, error) {
		fetches++
		return webaccess.Page{URL: "https://example.com/original", Text: "original body", Untrusted: true}, nil
	}
	for range 2 {
		results, err := executor.Execute(ctx, exchange, calls)
		if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" || fetches != 1 {
			raw, _ := json.Marshal(results)
			t.Fatal(string(raw), fetches, err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("bad source proof published")
	}
}

func TestWebPluginFetchLateForgetAndGuardianBeforePublication(t *testing.T) {
	for _, point := range []string{"after_web_fetch_method", "before_web_fetch_commit"} {
		for _, mode := range []string{"forget", "quarantine"} {
			t.Run(point+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				cfg := webaccess.Config{}
				calls := []llm.ToolCall{{ID: "private-fetch", Name: webaccess.FetchName, Arguments: `{"url":"https://example.com/private"}`}}
				var h rsi.Host
				var revision string
				s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
					r.ConfigureWebAccess(cfg, nil)
					h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
					status, err := h.Store.Status(ctx)
					if err != nil {
						t.Fatal(err)
					}
					revision = status.Revision
				})
				defer r.Release(ctx, claim)
				fetches, reached := 0, 0
				r.webFetch = func(context.Context, string) (webaccess.Page, error) {
					fetches++
					return webaccess.Page{Text: "PRIVATE_FETCH_BODY", URL: "https://example.com/private", Untrusted: true}, nil
				}
				owner := idAgent(t, s)
				s.MainBoundary = func(boundary string) {
					if boundary != point {
						return
					}
					s.MainBoundary = nil
					reached++
					if mode == "forget" {
						if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
							t.Fatal(err)
						}
					} else if err := h.Store.Quarantine(ctx, revision); err != nil {
						t.Fatal(err)
					}
				}
				_, err := executor.Execute(ctx, exchange, calls)
				want := agent.ErrClaimLost
				if mode == "quarantine" {
					want = rsi.ErrRevoked
				}
				if fetches != 1 || reached != 1 || !errors.Is(err, want) || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_FETCH_BODY')>0`) != 0 {
					t.Fatal(fetches, reached, err)
				}
			})
		}
	}
}

type webFactsSecret string

func (s webFactsSecret) Get(_ context.Context, ref string) ([]byte, error) {
	if ref != "fixture-key" {
		return nil, errors.New("wrong secret reference")
	}
	return []byte(s), nil
}
func (webFactsSecret) Put(context.Context, string, []byte) error { return errors.New("read only") }
func (webFactsSecret) Delete(context.Context, string) error      { return errors.New("read only") }

func TestWebPluginSourceFormattingActualHTTPUpgradeRawRowsAndRetainedRun(t *testing.T) {
	ctx := context.Background()
	secret := "private+key/!"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		requests.Add(1)
		if q.Method != "GET" || q.URL.Path != "/search" || q.URL.Query().Get("q") != "public query" || q.Header.Get("Authorization") != "Bearer "+secret {
			t.Error(q.Method, q.URL, q.Header.Get("Authorization") == "Bearer "+secret)
		}
		rows := []webaccess.SourceRow{}
		for index := range 25 {
			rows = append(rows, webaccess.SourceRow{URL: fmt.Sprintf("https://example.com/%d", index), Title: strings.Repeat("T", 3000) + secret, Content: strings.Repeat("c", 3000) + "AFTER_3000" + strings.Repeat("d", 2700)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Results []webaccess.SourceRow `json:"results"`
		}{rows})
	}))
	defer server.Close()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: server.URL, SearXNGSecretRef: "fixture-key"}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil, webFactsSecret(secret))
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:3]
	policy.Cases = policy.Cases[:4]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.1"))
	old, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "public evidence observed"}, nil
		}
		return calls(webaccess.SearchName, `{"query":"public query"}`), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Search the original public query").RunID }
	check := func(id string, titleBytes int, marker bool, firstURL string) {
		t.Helper()
		before := requests.Load()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.SearchName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var envelope webaccess.ResearchEnvelope
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &envelope) != nil || len(envelope.Queries) != 1 || len(envelope.Queries[0].Sources) != 5 || len(envelope.Queries[0].Sources[0].Title) != titleBytes || envelope.Queries[0].Sources[0].URL != firstURL || requests.Load() != before+1 {
			t.Fatal(raw, requests.Load(), before)
		}
		var full webaccess.Document
		decodeErr := json.Unmarshal([]byte(readArtifact(t, s, toolRunScope(t, s, id), envelope.ResponseID)), &full)
		encodedFull, _ := json.Marshal(full)
		if decodeErr != nil || len(full.Queries) != 1 || strings.Contains(full.Queries[0].Text, "AFTER_3000") != marker || strings.Contains(string(encodedFull), secret) {
			t.Fatal("formatting used cropped host data or leaked credential")
		}

	}
	native := queue("source-original-three-service")
	first, _ := selectWebVersion(t, h, old.Revision, "1.0.0", 5, 0)
	check(native, 1024, false, "https://example.com/0")
	queued := queue("source-queued-1024")
	second, pkg := selectWebVersion(t, h, first, "1.15.0", 5, 0)
	check(queued, 1024, false, "https://example.com/0")
	check(queue("source-title-and-snippet-upgrade"), 2048, true, "https://example.com/0")
	third, _ := selectWebVersion(t, h, second, "1.16.0", 5, 0)
	check(queue("source-previously-discarded-rows"), 1024, false, "https://example.com/24")
	activateDelegation(t, h, "source-format-rollback", third, first, "web-search-owner-1.0.0")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(queue("source-format-restored"), 1024, false, "https://example.com/0")
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("pure formatting added an opaque effect journal")
	}
}

func TestWebPluginRawSourceFactsKeepFullSanitizedRows(t *testing.T) {
	ctx := context.Background()
	secret := "private+key/!"
	for _, provider := range []string{"searxng", "tavily"} {
		t.Run(provider, func(t *testing.T) {
			rows := []webaccess.SourceRow{}
			for index := range 25 {
				rows = append(rows, webaccess.SourceRow{URL: fmt.Sprintf("https://example.com/%d", index), Title: strings.Repeat("T", 2000) + secret, Content: strings.Repeat("c", 4000) + url.QueryEscape(secret) + " " + url.PathEscape(secret)})
			}
			rows = append(rows, webaccess.SourceRow{URL: "https://example.com/?key=" + url.QueryEscape(secret), Title: "must not reach candidate"})
			payload, _ := json.Marshal(struct {
				Results []webaccess.SourceRow `json:"results"`
			}{rows})
			client := &http.Client{Transport: webRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer "+secret {
					t.Fatal("missing original credential")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: request}, nil
			})}
			cfg := webaccess.Config{SourceOrder: []string{provider}, SearXNGURL: "https://search.example", SearXNGSecretRef: "fixture-key", TavilySecretRef: "fixture-key"}
			q := webaccess.SearchQuery{Query: "public query", NumResults: 5}
			facts, err := webaccess.ReadSourceFacts(ctx, cfg, provider, q, nil, webFactsSecret(secret), client)
			if err != nil || facts.Format != "json" || len(facts.Rows) != 25 || len(facts.Rows[24].Title) <= 1024 || len(facts.Rows[24].Content) <= 2048 || len(facts.Sources) != 0 {
				t.Fatal(len(facts.Rows), err)
			}
			all, _ := json.Marshal(facts.Rows)
			for _, token := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
				if strings.Contains(string(all), token) {
					t.Fatal("credential crossed raw fact boundary")
				}
			}
			legacy, err := webaccess.ReadSource(ctx, cfg, provider, q, nil, webFactsSecret(secret), client)
			if err != nil || len(legacy.Sources) != 20 || len(legacy.Sources[0].Title) > 1024 || len(legacy.Text) > 20000 {
				t.Fatal("historical formatting changed", len(legacy.Sources), len(legacy.Text), err)
			}
		})
	}
}

func TestWebPluginSourceFormattingAcceptedBadPlanCannotReleaseOrReplay(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"url":"https://example.com/source","title":"original title","content":"original content"}]}`)
	}))
	defer server.Close()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: server.URL}
	calls := []llm.ToolCall{{ID: "bad-format", Name: webaccess.SearchName, Arguments: `{"query":"public query"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		initial, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectWebVersion(t, h, initial.Revision, "1.17.0", 5, 0)
	})
	defer r.Release(ctx, claim)
	for range 2 {
		results, err := executor.Execute(ctx, exchange, calls)
		if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || requests.Load() != 1 {
			raw, _ := json.Marshal(results)
			t.Fatal(string(raw), requests.Load(), err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("unproved source formatting became fact")
	}
}

func TestWebPluginSourceFormattingLargeSanitizedInputUsesOriginalDescriptor(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Results []webaccess.SourceRow `json:"results"`
		}{[]webaccess.SourceRow{{URL: "https://example.com/source", Title: strings.Repeat("Z", 200000), Content: "original content"}}})
	}))
	defer server.Close()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: server.URL, SearXNGSecretRef: "fixture-key"}
	calls := []llm.ToolCall{{ID: "large-format", Name: webaccess.SearchName, Arguments: `{"query":"public query"}`}}
	_, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil, webFactsSecret("Z"))
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	defer keepRunClaim(r, claim)()
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil || requests.Load() != 1 {
		raw, _ := json.Marshal(results)
		t.Fatal(string(raw), requests.Load(), err)
	}
	var value webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(results[0].Text), &value) != nil || len(value.Queries) != 1 || len(value.Queries[0].Sources) != 1 || len(value.Queries[0].Sources[0].Title) != 1024 || !strings.HasPrefix(value.Queries[0].Sources[0].Title, "[redacted]") {
		t.Fatal("sanitized source facts shrank to inline frame limit")
	}
}

func TestWebPluginSourceFormattingExaOriginalLimitAndIndependentUpgrade(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{SourceOrder: []string{"exa"}}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil)
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := "Title: " + strings.Repeat("T", 3000) + "\nURL: https://example.com/\nText: " + strings.Repeat("x", 22000)
	sourceCalls := 0
	client := &http.Client{Transport: webRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://mcp.exa.ai/mcp" || request.Header.Get("Authorization") != "" {
			t.Fatal(request.URL, "foreign credential")
		}
		var message struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.NewDecoder(request.Body).Decode(&message) != nil {
			t.Fatal("invalid RPC")
		}
		var value any
		switch message.Method {
		case "initialize":
			value = map[string]any{"protocolVersion": "2025-03-26"}
		case "notifications/initialized":
			return &http.Response{StatusCode: 202, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		case "tools/list":
			value = map[string]any{"tools": []any{map[string]any{"name": "web_search_exa", "inputSchema": map[string]any{"properties": map[string]any{"query": map[string]string{"type": "string"}}}}}}
		case "tools/call":
			sourceCalls++
			var params struct {
				Arguments struct {
					Query      string `json:"query"`
					NumResults int    `json:"numResults"`
				} `json:"arguments"`
			}
			if json.Unmarshal(message.Params, &params) != nil || params.Arguments.Query != "public query" || params.Arguments.NumResults != 5 {
				t.Fatal(string(message.Params))
			}
			value = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		default:
			t.Fatal(message.Method)
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": value})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: request}, nil
	})}
	r.webSourceRead = func(c context.Context, source string, q webaccess.SearchQuery, hosted webaccess.QuerySearch) (webaccess.SourceObservation, error) {
		return webaccess.ReadSourceFacts(c, cfg, source, q, hosted, nil, client)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "source outcome observed"}, nil
		}
		return calls(webaccess.SearchName, `{"query":"public query"}`), nil
	}
	check := func(key string, want bool) {
		t.Helper()
		id := mustSubmit(t, a, key, "Read original Exa evidence").RunID
		before := sourceCalls
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.SearchName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var value webaccess.ResearchEnvelope
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || sourceCalls != before+1 {
			t.Fatal(raw, sourceCalls, before)
		}
		var doc webaccess.Document
		if json.Unmarshal([]byte(readArtifact(t, s, toolRunScope(t, s, id), value.ResponseID)), &doc) != nil || len(doc.Queries) != 1 {
			t.Fatal("source document absent")
		}
		if want {
			if doc.Queries[0].Text != text || len(doc.Queries[0].Sources) != 1 || len(doc.Queries[0].Sources[0].Title) != 2048 {
				t.Fatal("Exa remained cropped before plugin")
			}
		} else {
			if len(value.Errors) != 1 || value.Errors[0].Code != "OUTPUT_LIMIT" || doc.Queries[0].Text != "" {
				t.Fatal("original Exa limit changed", value.Errors)
			}
		}
	}
	check("exa-original-format", false)
	selectWebVersion(t, h, first.Revision, "1.15.0", 5, 0)
	check("exa-larger-format", true)
}

func TestWebPluginSourceFormattingLateForgetBeforeSourceRelease(t *testing.T) {
	for _, mode := range []string{"forget", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"results":[{"url":"https://example.com/source","title":"PRIVATE_NORMALIZED_TITLE","content":"PRIVATE_NORMALIZED_BODY"}]}`)
			}))
			defer server.Close()
			cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: server.URL}
			calls := []llm.ToolCall{{ID: "late-format", Name: webaccess.SearchName, Arguments: `{"query":"public query"}`}}
			var h rsi.Host
			var revision string
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				status, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				revision = status.Revision
			})
			defer r.Release(ctx, claim)
			owner := idAgent(t, s)
			reached := 0
			s.MainBoundary = func(point string) {
				if point != "after_web_source_format" {
					return
				}
				s.MainBoundary = nil
				reached++
				if mode == "forget" {
					if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
						t.Fatal(err)
					}
				} else if err := h.Store.Quarantine(ctx, revision); err != nil {
					t.Fatal(err)
				}
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if mode == "forget" {
				if !errors.Is(err, agent.ErrClaimLost) {
					t.Fatal(results, err)
				}
			} else {
				if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" {
					t.Fatal(results, err)
				}
			}
			if reached != 1 || requests.Load() != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_NORMALIZED')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal("late normalized source accepted", reached, requests.Load())
			}
		})
	}
}

func TestWebPluginFetchRealReleaseRecoveryKeepsOriginalPreviewAndReceipt(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "recover-fetch", Name: webaccess.FetchName, Arguments: `{"url":"https://example.com/original"}`}}
	var h rsi.Host
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	stopRenew := keepRunClaim(r, claim)
	defer stopRenew()
	initial, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil || len(accepted) != 1 {
		t.Fatal(accepted, err)
	}
	if err = executor.Store.Start(ctx, accepted[0].ID); err != nil {
		t.Fatal(err)
	}
	selectWebVersion(t, h, initial.Revision, "1.13.0", 5, 0)
	stopRenew()
	if err = r.Release(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if pending, err := r.Next(ctx); err != nil || pending != claim.RunID() {
		t.Fatal("original execution did not recover", pending, err)
	}
	next, err := r.Claim(ctx, claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	executor.Store = r.ToolStore(next)
	full := strings.Repeat("a", 12000) + " ORIGINAL_END"
	fetches := 0
	r.webFetch = func(context.Context, string) (webaccess.Page, error) {
		fetches++
		return webaccess.Page{URL: "https://example.com/original", Text: full, Untrusted: true}, nil
	}
	first, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(first) != 1 || first[0].Error != nil {
		raw, _ := json.Marshal(first)
		t.Fatal(string(raw), err)
	}
	var value struct {
		Text string `json:"text"`
		Ref  string `json:"artifact_ref"`
		Next int    `json:"next_offset"`
	}
	if json.Unmarshal([]byte(first[0].Text), &value) != nil || len(value.Text) != 4096 || value.Next != 4096 || value.Ref == "" || readArtifact(t, s, toolRunScope(t, s, next.RunID()), value.Ref) != full {
		t.Fatal("recovery rebound to active preview", value.Next)
	}
	again, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(again) != 1 || again[0].Text != first[0].Text || fetches != 1 {
		t.Fatal("completed fetch receipt replayed HTTP", again, fetches, err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='succeeded'`, accepted[0].ID) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("recovery created another receipt")
	}
}
