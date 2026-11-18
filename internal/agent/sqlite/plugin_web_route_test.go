package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func installRouteNode(t *testing.T, s *Store, r *Runs, cfg webaccess.Config) (rsi.Host, string) {
	t.Helper()
	ctx := context.Background()
	r.ConfigureWebAccess(cfg, nil)
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(cfg.ExtractScript)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
	if err != nil {
		t.Fatal(err)
	}
	revision, _, err := selectExtract(t, h, cfg, resource.Digest(), status.Revision, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return h, revision
}

func upgradeRoute(t *testing.T, h rsi.Host, cfg webaccess.Config, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, webVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	old, err := h.Store.Graph(ctx, baseline)
	if err != nil {
		t.Fatal(err)
	}
	graph := old.Graph
	for i := range graph.Bindings {
		if graph.Bindings[i].InstanceID == "web-access" {
			graph.Bindings[i].PackageDigest = pkg.Digest
		}
	}
	prepared, err := h.Prepare(ctx, graph)
	if err != nil {
		t.Fatal(err)
	}
	policy := extractPolicy(t, cfg, "1.0.0")
	policy.ID = "route-owner-" + version
	if version == "1.25.0" {
		policy.Cases[9].Expected = json.RawMessage(`{"decoder":"html","error_code":""}`)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "route-upgrade-"+version, baseline, prepared.Revision, policy.ID)
	return prepared.Revision, pkg.Digest
}

// The same post-transport decoder used by actual FetchContent; only HTTP is
// replaced with deterministic original response facts/bytes in this carrier.
func routePageSource(r *Runs, cfg webaccess.Config) *int {
	count := new(int)
	r.webContentFetch = func(ctx context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		*count++
		return webcontent.DecodePage(ctx, cfg.Extraction(), webaccess.Page{URL: url, MediaType: "text/html", RetrievedAt: "2026-11-09T00:00:00Z", Freshness: "original fetched evidence", Untrusted: true}, "content", mode, []byte(extractHTML()))
	}
	return count
}

func routeResult(t *testing.T, s *Store, run string) (tool.Result, webaccess.ResearchEnvelope) {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, run, webaccess.ContentName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var envelope webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(raw), &result) != nil {
		t.Fatal(raw)
	}
	if result.Error == nil && json.Unmarshal([]byte(result.Text), &envelope) != nil {
		t.Fatal(raw)
	}
	return result, envelope
}

func TestWebPluginRouteActualNodeChoiceUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	h, original := installRouteNode(t, s, r, cfg)
	fetches := routePageSource(r, cfg)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "complete"}, nil
		}
		return calls(webaccess.ContentName, `{"url":"https://example.com/source","mode":"readable"}`), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Read original source").RunID }
	check := func(id string, markdown bool) {
		t.Helper()
		mustDrain(t, a)
		result, value := routeResult(t, s, id)
		if result.Error != nil || len(value.Pages) != 1 {
			t.Fatal(result)
		}
		page := value.Pages[0]
		if strings.Contains(page.Text, "# Evidence") != markdown || !strings.Contains(page.Text, "original reference") || page.URL != "https://example.com/source" || page.RetrievedAt != "2026-11-09T00:00:00Z" || !page.Untrusted {
			t.Fatal(page)
		}
		var full webaccess.Document
		if json.Unmarshal([]byte(readArtifact(t, s, toolRunScope(t, s, id), value.ResponseID)), &full) != nil || len(full.Pages) != 1 || full.Pages[0].Text != page.Text {
			t.Fatal("original evidence publication changed")
		}
	}
	old := queue("route-old-method")
	changed, pkg := upgradeRoute(t, h, cfg, original, "1.25.0")
	check(old, true)
	check(queue("route-new-method"), false)
	retained := queue("route-retained-method")
	activateDelegation(t, h, "route-rollback", changed, original, "web-extract-owner-1.0.0")
	if err := h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(retained, false)
	check(queue("route-rollback-method"), true)
	retained = queue("route-before-disable")
	if _, err := h.Store.Deactivate(ctx, "route-disable", original); err != nil {
		t.Fatal(err)
	}
	check(retained, true)
	before := *fetches
	id := queue("route-disabled")
	mustDrain(t, a)
	result, _ := routeResult(t, s, id)
	if result.Error == nil || *fetches != before || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(result, *fetches, before)
	}
}

func TestWebPluginRouteHistoricalSevenServiceRawRunKeepsOriginalBytes(t *testing.T) {
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil)
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:7]
	policy.Cases = policy.Cases[:8]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.4"))
	status, err := h.Store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	routePageSource(r, cfg)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "complete"}, nil
		}
		return calls(webaccess.ContentName, `{"url":"https://example.com/raw","mode":"raw"}`), nil
	}
	old := mustSubmit(t, a, "route-seven-service", "Read raw source").RunID
	selectWebVersion(t, h, status.Revision, "1.0.0", 5, 0)
	mustDrain(t, a)
	result, value := routeResult(t, s, old)
	if result.Error != nil || len(value.Pages) != 1 || value.Pages[0].Text != extractHTML() {
		t.Fatal(result)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND managed_services_json LIKE '%steward.web-content-route/1%'`, old) != 0 {
		t.Fatal("old service set rewritten")
	}
}

func TestWebPluginRouteAcceptedInvalidRawPlanCannotFallbackOrReplay(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "route-raw-invalid", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/raw","mode":"raw"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		revision, _ := selectWebVersion(t, h, status.Revision, "1.26.0", 5, 0)
		raw, err := h.ReadService(ctx, revision, webaccess.RouteContract, json.RawMessage(`{"operation":"content","media_type":"text/html","mode":"raw","document_available":false}`), nil)
		var plan webcontent.RoutePlan
		if err != nil || json.Unmarshal(raw, &plan) != nil || plan.Decoder != "html" {
			t.Fatal("candidate did not produce the reproduced invalid raw plan", string(raw), err)
		}
	})
	defer r.Release(ctx, claim)
	fetches := routePageSource(r, cfg)
	routed := 0
	s.MainBoundary = func(point string) {
		if point == "after_web_route_method" {
			routed++
		}
	}
	for i := 0; i < 2; i++ {
		results, err := executor.Execute(ctx, exchange, calls)
		if err != nil || len(results) != 1 || results[0].Error == nil {
			t.Fatal(results, err)
		}
	}
	if *fetches != 1 || routed != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(*fetches, routed)
	}
}

func TestWebPluginRouteLateForgetAndGuardianBeforeDecode(t *testing.T) {
	for _, mode := range []string{"forget", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{}
			calls := []llm.ToolCall{{ID: "route-private", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/private"}`}}
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
			fetches := routePageSource(r, cfg)
			owner := idAgent(t, s)
			reached, decoded := 0, 0
			fetch := r.webContentFetch
			r.webContentFetch = func(c context.Context, cfg webcontent.Config, url, mode string) (webaccess.Page, error) {
				c = webcontent.WithHTMLParser(c, func(context.Context, []byte) (string, string, error) { decoded++; return "", "private body", nil })
				return fetch(c, cfg, url, mode)
			}
			s.MainBoundary = func(point string) {
				if point != "after_web_route_method" {
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
			if err == nil && (len(results) != 1 || results[0].Error == nil) {
				t.Fatal(results)
			}
			if reached != 1 || *fetches != 1 || decoded != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal(reached, *fetches, decoded, results, err)
			}
		})
	}
}

func TestWebPluginRouteChildRetainsOriginalRouteAndNodeAfterUpgrade(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	h, original := installRouteNode(t, s, r, cfg)
	graph, err := h.Store.Graph(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	var originalPackage string
	for _, binding := range graph.Graph.Bindings {
		if binding.InstanceID == "web-access" {
			originalPackage = binding.PackageDigest
		}
	}
	fetches := routePageSource(r, cfg)
	switched := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main delivery"}, nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Child read complete"}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"PRIVATE_PARENT_ROUTE"}`), nil
		}
		if last.Text == "read child source" {
			return calls(webaccess.ContentName, `{"url":"https://example.com/child"}`), nil
		}
		if !switched {
			upgradeRoute(t, h, cfg, original, "1.25.0")
			if err := h.Store.Retire(ctx, originalPackage); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.ExtractScript, []byte("replacement script"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.ExtractCommand, []byte("replacement interpreter"), 0700); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		return calls(agent.RunToolName, `{"task":"read child source"}`), nil
	}
	mustSubmit(t, a, "route-child-old", "PRIVATE_MAIN_ROUTE")
	mustDrain(t, a)
	if !switched || *fetches != 1 || scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.worker_position='child' AND c.status='completed' AND cb.graph_revision=? AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json`, original) != 1 {
		t.Fatal(switched, *fetches)
	}
	var childID, raw string
	if err := s.db.QueryRow(`SELECT r.run_id,i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name=? AND i.status='succeeded'`, webaccess.ContentName).Scan(&childID, &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "# Evidence") {
		t.Fatal("Child lost original route/decoder", raw)
	}
	p.mu.Lock()
	requests := append([]llm.GenerationRequest{}, p.requests...)
	p.mu.Unlock()
	seen := 0
	for _, request := range requests {
		if request.RunID != childID {
			continue
		}
		seen++
		raw, _ := json.Marshal(request.Messages)
		if bytes.Contains(raw, []byte("PRIVATE_PARENT_ROUTE")) || bytes.Contains(raw, []byte("PRIVATE_MAIN_ROUTE")) {
			t.Fatal("private parent context reached Child")
		}
	}
	if seen == 0 {
		t.Fatal("no actual Child requests")
	}
}

func TestWebPluginRouteRealReleaseRecoveryDoesNotReplayNeverTool(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "route-recovery", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/recover"}`}}
	var h rsi.Host
	var original string
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		original = status.Revision
	})
	stop := keepRunClaim(r, claim)
	defer stop()
	fetches := routePageSource(r, cfg)
	reached := 0
	s.MainBoundary = func(point string) {
		if point != "after_web_route_method" {
			return
		}
		s.MainBoundary = nil
		reached++
		stop()
		if err := r.Release(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, agent.ErrClaimLost) || reached != 1 || *fetches != 1 {
		t.Fatal(reached, *fetches, err)
	}
	selectWebVersion(t, h, original, "1.26.0", 5, 0)
	if id, err := r.Next(ctx); err != nil || id != claim.RunID() {
		t.Fatal(id, err)
	}
	next, err := r.Claim(ctx, claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	executor.Store = r.ToolStore(next)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || *fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(results, *fetches, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=?`, claim.RunID(), original) != 1 {
		t.Fatal("recovery rewrote original route graph")
	}
}
