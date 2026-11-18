package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/plugin/webhtml"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func TestWebPluginHTMLActualUpgradeRetainedRunRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	activateLoop(t, s, modelInput())
	r.ConfigureWebAccess(cfg, nil)
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:5]
	policy.Cases = policy.Cases[:6]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.2"))
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r.webFetch = func(c context.Context, url string) (webaccess.Page, error) {
		fetches++
		title, text, err := webcontent.ParseHTML(c, []byte(`<title>Original &amp; title</title><nav>NAV POLICY</nav><article>original body</article><script>secret()</script>`))
		return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true, RetrievedAt: "2026-11-09T00:00:00Z"}, err
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "done"}, nil
		}
		return calls(webaccess.FetchName, `{"url":"https://example.com/original"}`), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Read original page").RunID }
	check := func(id, want string) {
		t.Helper()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.FetchName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var page struct {
			webaccess.Page
			Ref string `json:"artifact_ref"`
		}
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &page) != nil || page.Text != want || page.Title != "Original & title" || page.Ref == "" || readArtifact(t, s, toolRunScope(t, s, id), page.Ref) != want {
			t.Fatal(raw, want)
		}
	}
	native := queue("html-before-managed-parser")
	baseline, _ := selectWebVersion(t, h, status.Revision, "1.0.0", 5, 0)
	check(native, "original body")
	old := queue("html-before-upgrade")
	revision, digest := selectWebVersion(t, h, baseline, "1.18.0", 5, 0)
	check(old, "original body")
	check(queue("html-new-policy"), "NAV POLICY original body")
	activateDelegation(t, h, "html-rollback", revision, baseline, "web-search-owner-1.0.0")
	if err = h.Store.Retire(ctx, digest); err != nil {
		t.Fatal(err)
	}
	check(queue("html-rollback-run"), "original body")
	retained := queue("html-before-disable")
	if _, err = h.Store.Deactivate(ctx, "html-disable", baseline); err != nil {
		t.Fatal(err)
	}
	check(retained, "original body")
	before := fetches
	disabled := queue("html-disabled-new-run")
	mustDrain(t, a)
	if before != fetches || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND status='failed'`, disabled) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("disabled parser silently used native", fetches, before)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) c WHERE r.run_id=? AND c.value=?`, native, webhtml.Contract) != 0 {
		t.Fatal("historical parser retroactively migrated")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) c WHERE r.run_id=? AND c.value=?`, old, webhtml.Contract) != 1 {
		t.Fatal("parser not frozen")
	}
}

func TestWebPluginHTMLCompleteEightMiBSourceAndDerivedText(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "large-html", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/full"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	stop := keepRunClaim(r, claim)
	defer stop()
	// The original 8 MiB raw input is attached directly, without base64/JSON inflation.
	prefix, suffix := `<title>large source</title><article>`, `</article>`
	body := strings.Repeat("x", (8<<20)-len(prefix)-len(suffix))
	html := []byte(prefix + body + suffix)
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		title, text, err := webcontent.ParseHTML(c, html)
		return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	var value webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(results[0].Text), &value) != nil || value.ResponseID == "" {
		t.Fatal(results[0].Text)
	}
	var saved webaccess.Document
	full := readArtifact(t, s, toolRunScope(t, s, claim.RunID()), value.ResponseID)
	if json.Unmarshal([]byte(full), &saved) != nil || len(saved.Pages) != 1 || saved.Pages[0].Text != body || saved.Pages[0].Title != "large source" {
		t.Fatal("complete original derived text lost", len(full))
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("parser created another durable operation")
	}
}

func TestWebPluginHTMLLateForgetAndGuardianCannotReleaseDerivedText(t *testing.T) {
	for _, mode := range []string{"forget", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{}
			calls := []llm.ToolCall{{ID: "private-html", Name: webaccess.FetchName, Arguments: `{"url":"https://example.com/private"}`}}
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
			reached, fetches := 0, 0
			r.webFetch = func(c context.Context, url string) (webaccess.Page, error) {
				fetches++
				title, text, err := webcontent.ParseHTML(c, []byte(`<article>PRIVATE_HTML_TEXT</article>`))
				return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
			}
			owner := idAgent(t, s)
			s.MainBoundary = func(point string) {
				if point != "after_web_html_method" {
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
			if mode == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(results, err)
			}
			if mode == "quarantine" && err != nil && !errors.Is(err, rsi.ErrRevoked) {
				t.Fatal(results, err)
			}
			if mode == "quarantine" && err == nil && (len(results) != 1 || results[0].Error == nil) {
				t.Fatal(results)
			}
			if reached != 1 || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_HTML_TEXT')>0`) != 0 {
				t.Fatal(reached, fetches, results, err)
			}
		})
	}
}
