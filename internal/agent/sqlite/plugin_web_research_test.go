package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func TestWebPluginResearchActualBatchSummaryAndCompleteEvidence(t *testing.T) {
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, p)
	installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	queries := []string{}
	snippet := strings.Repeat("原始检索证据 ", 800)
	r.webSourceRead = func(_ context.Context, provider string, q webaccess.SearchQuery, _ webaccess.QuerySearch) (webaccess.SourceObservation, error) {
		if provider != "searxng" || q.NumResults != 5 {
			t.Fatal(provider, q)
		}
		queries = append(queries, q.Query)
		return webaccess.SourceObservation{Text: snippet, Sources: []llm.WebSource{{URL: "https://example.com/article", Title: "原资料"}, {URL: "https://example.com/unavailable"}}, RetrievedAt: "2026-11-09T00:00:00Z"}, nil
	}
	full := strings.Repeat("完整正文🧭 ", 2000) + "original-final-marker"
	fetches := []string{}
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		fetches = append(fetches, url)
		if mode != "" {
			t.Fatal(mode)
		}
		if strings.HasSuffix(url, "unavailable") {
			return webaccess.Page{}, errors.New("WEB_HTTP_OR_ACCESS_ERROR")
		}
		return webaccess.Page{URL: url, Text: full, Title: "原始标题", Untrusted: true, RetrievedAt: "2026-11-09T00:01:00Z", Freshness: "fetched_now; origin may serve cached content"}, nil
	}
	models := 0
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {

		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "complete evidence retained"}, nil
		}
		return calls(webaccess.SearchName, `{"queries":["first query","second query"],"includeContent":true,"workflow":"summary-review"}`), nil
	})
	// Auxiliary model calls have a concrete kind; route those before the Main /
	// Root role fixture, which otherwise interprets every call as task control.
	respond := p.respond
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.ToolGeneration != nil {
			models++
			if req.ToolGeneration.Kind != "summary" || req.ToolGeneration.CallKey != "summary" || len(req.Tools) != 0 || len(req.Messages) != 2 || !strings.Contains(req.Messages[1].Text, "原始检索证据") {
				t.Fatal(req.ToolGeneration)
			}
			return llm.GenerationResult{Text: "原摘要草稿 https://example.com/article", Usage: &llm.TokenUsage{InputTokens: 7, OutputTokens: 2}}, nil
		}
		return respond(req)
	}
	mustSubmit(t, a, "managed-full-research", "Collect both public queries and retain full sources")
	mustDrain(t, a)
	if strings.Join(queries, ",") != "first query,second query" || len(fetches) != 2 || models != 1 {
		t.Fatal(queries, fetches, models)
	}
	var raw, runID string
	if err := s.db.QueryRow(`SELECT result_json,run_id FROM tool_invocation WHERE tool_name=?`, webaccess.SearchName).Scan(&raw, &runID); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil {
		t.Fatal(raw)
	}
	var envelope webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(result.Text), &envelope) != nil || envelope.ResponseID == "" || len(envelope.Queries) != 2 || len(envelope.Pages) != 1 || len(envelope.Errors) != 1 || envelope.Errors[0].URL != "https://example.com/unavailable" || !envelope.ReviewRequired || envelope.Summary != "原摘要草稿 https://example.com/article" || len(envelope.Pages[0].Text) > 4096 {
		t.Fatal(result.Text)
	}
	body := readArtifact(t, s, toolRunScope(t, s, runID), envelope.ResponseID)
	var document webaccess.Document
	if json.Unmarshal([]byte(body), &document) != nil || len(document.Pages) != 1 || document.Pages[0].Text != full || len(document.Queries) != 2 || document.Queries[0].Text != snippet {
		t.Fatal("complete evidence changed")
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.call_key')='summary' AND status='completed' AND json_extract(result_json,'$.usage.input_tokens')=7`, runID) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("original model ledger was bypassed")
	}
}

func TestWebPluginResearchPageAnswersUseOriginalFrozenModelAndReceipt(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "page-answers", Name: webaccess.ContentName, Arguments: `{"urls":["https://example.com/one","https://example.com/two"],"mode":"answer","prompt":"Which facts are explicit?"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	defer keepWebReadClaim(r, claim)()
	run, err := r.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	r.webProvider = r.GuardProvider(&loopProvider{store: s, respond: func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		modelCalls++
		before, _ := encoded(run.Snapshot)
		after, _ := encoded(req.Snapshot)
		if before != after || req.ToolGeneration == nil || req.ToolGeneration.CallKey != fmt.Sprintf("answer:%d", modelCalls-1) || req.ToolGeneration.Kind != "page_answer" || len(req.Tools) != 0 || len(req.Messages) != 2 || !strings.Contains(req.Messages[1].Text, "Which facts are explicit?") || !strings.Contains(req.Messages[1].Text, "untrusted_evidence") {
			t.Fatal(req.ToolGeneration, before, after)
		}
		return llm.GenerationResult{Text: fmt.Sprintf("original answer %d", modelCalls), Usage: &llm.TokenUsage{InputTokens: 3, OutputTokens: 2}}, nil
	}})
	fetches := 0
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		fetches++
		if mode != "answer" {
			t.Fatal(mode)
		}
		return webaccess.Page{URL: url, Text: "explicit public evidence", Untrusted: true}, nil
	}
	first, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(first) != 1 || first[0].Error != nil {
		raw, _ := json.Marshal(first)
		t.Fatal(string(raw), err)
	}
	var value webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(first[0].Text), &value) != nil || value.Summary != "original answer 1\n\noriginal answer 2\n\n" || len(value.Pages) != 2 || len(first[0].ArtifactRefs) != 1 {
		t.Fatal(first[0].Text)
	}
	again, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(again) != 1 || again[0].Text != first[0].Text || modelCalls != 2 || fetches != 2 {
		t.Fatal(again, modelCalls, fetches, err)
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.kind')='page_answer' AND status='completed'`, claim.RunID()) != 2 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("model receipt changed")
	}
}

func TestWebPluginResearchAssessmentHashesOriginalPageBytes(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
	calls := []llm.ToolCall{{ID: "source-check", Name: webaccess.CheckName, Arguments: `{"claim":"Does this source establish the claim?"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	r.webSourceRead = func(context.Context, string, webaccess.SearchQuery, webaccess.QuerySearch) (webaccess.SourceObservation, error) {
		return webaccess.SourceObservation{Text: "snippet", Sources: []llm.WebSource{{URL: "https://example.com/source"}}}, nil
	}
	full := strings.Repeat("原始文本🧭 ", 1000)
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		return webaccess.Page{URL: url, Text: full, Untrusted: true}, nil
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		raw, _ := json.Marshal(results)
		t.Fatal(string(raw), err)
	}
	var value webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(results[0].Text), &value) != nil || value.Status != "unclear" || value.Claim != "Does this source establish the claim?" || len(value.Passages) != 1 {
		t.Fatal(results[0].Text)
	}
	p := value.Passages[0]
	if p.Start != 0 || p.End > 1600 || p.Text != full[p.Start:p.End] || p.ContentHash != webaccess.Hash(p.Text) || p.SourceHash != webaccess.Hash(full) || p.URL != "https://example.com/source" {
		t.Fatal(p)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("assessment added an opaque ledger")
	}
}

func TestWebPluginResearchRealPreServiceRunUpgradePreviewRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil)
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:2]
	policy.Cases = policy.Cases[:2]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.0"))
	old, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("x", 10000) + "original end"
	fetches := 0
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		return webaccess.Page{URL: url, Text: full, Untrusted: true}, nil
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "original source retained"}, nil
		}
		return calls(webaccess.ContentName, `{"url":"https://example.com/evidence"}`), nil
	}
	queue := func(key string) string {
		t.Helper()
		return mustSubmit(t, a, key, "Read the explicit public source").RunID
	}
	check := func(id string, want int, managed bool) string {
		t.Helper()
		before := fetches
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.ContentName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var value webaccess.ResearchEnvelope
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || len(value.Pages) != 1 || len(value.Pages[0].Text) != want || value.Pages[0].Text != full[:want] || fetches != before+1 {
			t.Fatal("preview did not use original frozen implementation", raw, fetches, before)
		}
		if flag := scalar(t, s, `SELECT count(*) FROM rsi_run b,json_each(b.managed_services_json) s WHERE b.run_id=? AND s.value=?`, id, webaccess.ResearchContract); managed != (flag == 1) {
			t.Fatal("migration boundary rewritten", flag, managed)
		}
		var document webaccess.Document
		if json.Unmarshal([]byte(readArtifact(t, s, toolRunScope(t, s, id), value.ResponseID)), &document) != nil || len(document.Pages) != 1 || document.Pages[0].Text != full {
			t.Fatal("source truncated with preview")
		}
		return value.ResponseID
	}
	native := queue("research-original-two-services")
	first, _ := selectWebVersion(t, h, old.Revision, "1.0.0", 5, 0)
	check(native, 4096, false)
	queued := queue("research-queued-v1")
	second, pkg := selectWebVersion(t, h, first, "1.9.0", 5, 0)
	check(queued, 4096, true)
	latest := queue("research-new-preview")
	ref := check(latest, 1024, true)
	activateDelegation(t, h, "research-preview-rollback", second, first, "web-search-owner-1.0.0")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(queue("research-rollback-preview"), 4096, true)
	// Retirement changes neither original saved bytes nor its reader graph.
	if !strings.Contains(readArtifact(t, s, toolRunScope(t, s, latest), ref), "original end") {
		t.Fatal("retired evidence changed")
	}
	if _, err = h.Store.Deactivate(ctx, "research-disable", first); err != nil {
		t.Fatal(err)
	}
	before := fetches
	disabled := queue("research-disabled")
	mustDrain(t, a)
	if fetches != before || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='failed' AND json_extract(error_json,'$.code')='PROVIDER_UNAVAILABLE'`, disabled, webaccess.ContentName) != 1 {
		t.Fatal("native research fallback after deactivation", fetches, before)
	}
}

func TestWebPluginResearchAcceptedBadPackagesCannotFetchForgeOrSpend(t *testing.T) {
	for _, version := range []string{"1.10.0", "1.11.0", "1.12.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
			name, args := webaccess.ContentName, `{"url":"https://example.com/explicit"}`
			if version == "1.11.0" {
				name, args = webaccess.CheckName, `{"claim":"original claim"}`
			}
			if version == "1.12.0" {
				args = `{"url":"https://example.com/explicit","mode":"raw","prompt":"malicious"}`
			}
			calls := []llm.ToolCall{{ID: "proof-research", Name: name, Arguments: args}}
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				initial, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectWebVersion(t, h, initial.Revision, version, 5, 0)
			})
			defer r.Release(ctx, claim)
			defer keepWebReadClaim(r, claim)()
			sources, fetches, models := 0, 0, 0
			r.webSourceRead = func(context.Context, string, webaccess.SearchQuery, webaccess.QuerySearch) (webaccess.SourceObservation, error) {
				sources++
				return webaccess.SourceObservation{Text: "original source", Sources: []llm.WebSource{{URL: "https://example.com/source"}}}, nil
			}
			r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
				fetches++
				return webaccess.Page{URL: url, Text: "entire original page", Untrusted: true}, nil
			}
			r.webProvider = r.GuardProvider(&loopProvider{store: s, respond: func(llm.GenerationRequest) (llm.GenerationResult, error) {
				models++
				return llm.GenerationResult{Text: "must not be generated"}, nil
			}})
			results, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error == nil {
				raw, _ := json.Marshal(results)
				t.Fatal(string(raw), err)
			}
			wantFetches, wantSources, wantCode := 0, 0, "PROVIDER_UNAVAILABLE"
			if version == "1.11.0" {
				wantFetches, wantSources, wantCode = 1, 1, "UNKNOWN_SIDE_EFFECT"
			}
			if version == "1.12.0" {
				wantFetches = 1
			}
			if fetches != wantFetches || sources != wantSources || models != 0 || results[0].Error.Code != wantCode || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE json_extract(request_json,'$.tool_generation.kind') IS NOT NULL`) != 0 {
				t.Fatal(fetches, sources, models, results[0].Error)
			}
			if _, err = executor.Execute(ctx, exchange, calls); err != nil || fetches != wantFetches || sources != wantSources || models != 0 {
				t.Fatal("failed/unknown research replayed", fetches, sources, models, err)
			}
		})
	}
}

func TestWebPluginResearchLateForgetAndGuardianAtPublicationBoundary(t *testing.T) {
	for _, point := range []string{"after_web_research_method", "before_web_research_commit"} {
		for _, mode := range []string{"forget", "quarantine"} {
			t.Run(point+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				cfg := webaccess.Config{}
				calls := []llm.ToolCall{{ID: "private-publication", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/private-evidence"}`}}
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
				fetches := 0
				r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
					fetches++
					return webaccess.Page{URL: url, Text: "PRIVATE_RESEARCH_PUBLICATION", Untrusted: true}, nil
				}
				owner := idAgent(t, s)
				reached := 0
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
				results, err := executor.Execute(ctx, exchange, calls)
				want := agent.ErrClaimLost
				if mode == "quarantine" {
					want = rsi.ErrRevoked
				}
				if fetches != 1 || reached != 1 || !errors.Is(err, want) {
					raw, _ := json.Marshal(results)
					t.Fatal(fetches, reached, string(raw), err)
				}
				if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_RESEARCH_PUBLICATION')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
					t.Fatal("revoked result reached publication")
				}
			})
		}
	}
}

func TestWebPluginResearchBudgetApprovalBeforeAuxiliaryModel(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
	calls := []llm.ToolCall{{ID: "budgeted-summary", Name: webaccess.SearchName, Arguments: `{"query":"public query","workflow":"summary-review"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		r.ConfigureSpend(spendPolicy(t, 15000))
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	sources, models := 0, 0
	r.webSourceRead = func(context.Context, string, webaccess.SearchQuery, webaccess.QuerySearch) (webaccess.SourceObservation, error) {
		sources++
		return webaccess.SourceObservation{Text: "original source", Sources: []llm.WebSource{{URL: "https://example.com/source"}}}, nil
	}
	r.webProvider = r.GuardProvider(&loopProvider{store: s, respond: func(llm.GenerationRequest) (llm.GenerationResult, error) {
		models++
		return llm.GenerationResult{Text: "unapproved answer"}, nil
	}})
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" || models != 0 || sources != 1 {
		raw, _ := json.Marshal(results)
		t.Fatal(string(raw), models, sources, err)
	}
	// The existing fixed public error normalizer masks feature-specific codes.
	// The original durable spend approval, rather than that generic label, proves
	// the denial and remains the actionable owner UI/approval authority.
	if scalar(t, s, `SELECT count(*) FROM spend_approval WHERE status='pending'`) != 1 {
		t.Fatal("original budget approval missing")
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.call_key')='summary' AND status='failed' AND error_code='BUDGET_APPROVAL_REQUIRED'`, claim.RunID()) != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("spend gate or journal bypassed")
	}
}

// A process can return a body before its durable completion is committed. The
// original pending receipt is authority for uncertainty, not those loose bytes.
type webUnsettledModel struct{ calls int }

func (p *webUnsettledModel) Generate(context.Context, llm.GenerationRequest, func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	p.calls++
	return llm.GenerationResult{Text: "UNCOMMITTED_MODEL_TEXT"}, nil
}
func (p *webUnsettledModel) Continue(ctx context.Context, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.Generate(ctx, r, sink)
}
func (*webUnsettledModel) Cancel(context.Context, string) error  { return nil }
func (*webUnsettledModel) Dispose(context.Context, string) error { return nil }

func TestWebPluginResearchPendingModelCannotPublishOrRepeat(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "pending-answer", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/evidence","mode":"answer","prompt":"Read exact facts"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	provider := &webUnsettledModel{}
	r.webProvider = provider
	fetches := 0
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		return webaccess.Page{URL: url, Text: "original page", Untrusted: true}, nil
	}
	for iteration := 0; iteration < 2; iteration++ {
		results, err := executor.Execute(ctx, exchange, calls)
		if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || provider.calls != 1 || fetches != 1 {
			raw, _ := json.Marshal(results)
			t.Fatal(string(raw), provider.calls, fetches, err)
		}
		if iteration == 0 {
			if err = r.Release(ctx, claim); err != nil {
				t.Fatal(err)
			}
			next, err := r.Claim(ctx, claim.RunID())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Release(ctx, next)
			executor.Store = r.ToolStore(next)
		}

	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.call_key')='answer:0' AND status='pending'`, claim.RunID()) != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("unsettled model output became accepted fact")
	}
}

func TestWebPluginResearchLargeCompleteEvidenceDoesNotUsePureResultLimit(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	urls := []string{}
	for i := 0; i < 8; i++ {
		urls = append(urls, fmt.Sprintf("https://example.com/page-%d", i))
	}
	args, _ := json.Marshal(map[string]any{"urls": urls})
	calls := []llm.ToolCall{{ID: "large-research", Name: webaccess.ContentName, Arguments: string(args)}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	defer keepWebReadClaim(r, claim)()
	full := strings.Repeat("a", 5<<19) + " ORIGINAL_END"
	fetches := 0
	r.webContentFetch = func(_ context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		return webaccess.Page{URL: url, Text: full, Untrusted: true}, nil
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil || fetches != 8 || len(results[0].ArtifactRefs) != 1 {
		raw, _ := json.Marshal(results)
		t.Fatal(string(raw), fetches, err)
	}
	var preview webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(results[0].Text), &preview) != nil || len(preview.Pages) != 8 {
		t.Fatal("preview absent")
	}
	for _, page := range preview.Pages {
		if len(page.Text) != 4096 {
			t.Fatal(len(page.Text))
		}
	}
	body := readArtifact(t, s, toolRunScope(t, s, claim.RunID()), preview.ResponseID)
	var document webaccess.Document
	if len(body) <= 20<<20 || json.Unmarshal([]byte(body), &document) != nil || len(document.Pages) != 8 {
		t.Fatal("complete document shrank to plugin pure-input/result bound", len(body))
	}
	for i, page := range document.Pages {
		if page.Text != full || page.URL != urls[i] {
			t.Fatal("complete source missing", i, len(page.Text))
		}
	}
}
