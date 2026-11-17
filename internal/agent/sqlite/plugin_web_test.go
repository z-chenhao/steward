package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func webOwnerPolicy(count, index int) rsi.Policy {
	policy := builtin.WebPolicy()
	for _, position := range []int{0, 3} {
		var request webaccess.SourceRequest
		_ = json.Unmarshal(policy.Cases[position].Resources[0].Input, &request)
		request.NumResults = count
		policy.Cases[position].Resources[0].Input, _ = json.Marshal(request)
		var plan webaccess.SearchPlan
		_ = json.Unmarshal(policy.Cases[position].Expected, &plan)
		plan.Sources[0].Index = index
		policy.Cases[position].Expected, _ = json.Marshal(plan)
	}
	return policy
}
func webVersionPackage(t *testing.T, version string) string {
	t.Helper()
	directory := t.TempDir()
	replacements := map[string]string{}
	for _, name := range []string{"rsi.go", "search.go", "read_policy.go", "research_policy.go", "fetch_policy.go", "normalize_policy.go", "route_policy.go", "../webhtml/html.go", "../webimage/image.go"} {
		path, err := filepath.Abs("../../plugin/webaccess/" + name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := string(raw)
		if name == "../webhtml/html.go" && version == "1.18.0" {
			code = strings.Replace(code, `"nav", "footer",`, `"footer",`, 1)
		}
		if name == "../webimage/image.go" {
			if version == "1.20.0" {
				code = strings.ReplaceAll(code, "2000/float64", "1200/float64")
				code = strings.Replace(code, "Quality: 85", "Quality: 70", 1)
				code = strings.Replace(code, "Image fetched (%d x %d)", "Evidence image (%d x %d)", 1)
			}
			if version == "1.21.0" {
				code = strings.Replace(code, `"image/jpeg"`, "", 1)
				code = strings.Replace(code, `_ "image/png"`, `"image/png"`, 1)
				code = strings.Replace(code, `jpeg.Encode(&output, dst, &jpeg.Options{Quality: 85})`, `png.Encode(&output, dst)`, 1)
				code = strings.Replace(code, `MediaType: "image/jpeg"`, `MediaType: "image/png"`, 1)
			}
			if version == "1.22.0" {
				code = strings.Replace(code, "Width: width,", "Width: width+1,", 1)
			}
			if version == "1.23.0" {
				code = strings.Replace(code, `MediaType: "image/jpeg"`, `MediaType: "image/png"`, 1)
			}
			if version == "1.24.0" {
				code = strings.Replace(code, "Data: output.Bytes(),", "Data: output.Bytes()[:len(output.Bytes())-20],", 1)
			}
		}
		if name == "route_policy.go" {
			if version == "1.25.0" {
				code = strings.Replace(code, "if q.DocumentAvailable {", `if q.DocumentAvailable && q.MediaType != "text/html" {`, 1)
			}
			if version == "1.26.0" {
				code = strings.Replace(code, `if q.Mode == "raw" {`+"\n\t\t\treturn webcontent.RoutePlan{Decoder: \"text\"}", `if q.Mode == "raw" {`+"\n\t\t\treturn webcontent.RoutePlan{Decoder: \"html\"}", 1)
			}
		}
		if name == "rsi.go" {
			if strings.HasPrefix(version, "0.9.") {
				code = strings.Replace(code, ", RouteContract: routeService", "", 1)
			}
			code = strings.Replace(code, `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
			if version == "1.3.0" {
				code = strings.Replace(code, `Index: index,`, `Index: index+99,`, 1)
			}
			if version == "1.6.0" {
				code = strings.Replace(code, `[]string{"searxng", "native", "exa", "tavily"}`, `[]string{"tavily", "searxng", "native", "exa"}`, 1)
			}
			if version == "0.9.0" {
				code = strings.Replace(code, ", ResearchContract: researchService", "", 1)
			}
			if version == "0.9.0" || version == "0.9.1" {
				code = strings.Replace(code, ", FetchContract: fetchService", "", 1)
				code = strings.Replace(code, ", NormalizeContract: normalizeService", "", 1)
			}
			if version == "0.9.0" || version == "0.9.1" || version == "0.9.2" {
				code = strings.Replace(code, ", webhtml.Contract: webhtml.Service", "", 1)
				code = strings.Replace(code, `"github.com/z-chenhao/assistant/internal/plugin/webhtml"`, "", 1)
			}
			if version == "0.9.0" || version == "0.9.1" || version == "0.9.2" || version == "0.9.3" {
				code = strings.Replace(code, ", webimage.Contract: webimage.Service", "", 1)
				code = strings.Replace(code, `"github.com/z-chenhao/assistant/internal/plugin/webimage"`, "", 1)
			}
		} else if name == "fetch_policy.go" {
			if version == "1.13.0" {
				code = strings.Replace(code, "Preview(page.Page.Text, 4096)", "Preview(page.Page.Text, 1024)", 1)
			}
			if version == "1.14.0" {
				code = strings.Replace(code, "TextBytes: len(Preview(page.Page.Text, 4096))", "TextBytes: len(page.Page.Text)+(1<<20)", 1)
			}
		} else if name == "normalize_policy.go" {
			if version == "1.15.0" {
				code = strings.Replace(code, "LimitBytes: 20000", "LimitBytes: 30000", 1)
				code = strings.Replace(code, "Preview(row.Title, 1024)", "Preview(row.Title, 2048)", 1)
				code = strings.Replace(code, "Preview(row.Content, 2048)", "Preview(row.Content, 4096)", 1)
			}
			if version == "1.16.0" {
				code = strings.Replace(code, "for index, row := range input.Rows {", "for offset := range input.Rows { index:=len(input.Rows)-1-offset; row:=input.Rows[index]", 1)
			}
			if version == "1.17.0" {
				code = strings.Replace(code, "Index: index,", "Index: index+99,", 1)
			}
		} else if name == "research_policy.go" {
			if version == "1.9.0" {
				code = strings.Replace(code, "Preview(preview.Pages[i].Text, 4096)", "Preview(preview.Pages[i].Text, 1024)", 1)
			}
			if version == "1.10.0" {
				code = strings.Replace(code, "Source: index}", "Source: index+99}", 1)
			}
			if version == "1.11.0" {
				code = strings.Replace(code, "End: end}", "End: end+1}", 1)
			}
			if version == "1.12.0" {
				code = strings.Replace(code, `q.Input.Mode == "answer" {`, `(q.Input.Mode == "answer" || q.Input.Prompt == "malicious") {`, 1)
			}
		} else if name == "read_policy.go" {
			if version == "1.7.0" {
				code = strings.Replace(code, "limit = 8000", "limit = 6000", 1)
				code = strings.Replace(code, "start-400", "start-80", 1)
			}
			if version == "1.8.0" {
				code = strings.Replace(code, "plan.MatchStart, plan.MatchEnd = start, end", "plan.MatchStart, plan.MatchEnd = start+1, end", 1)
			}
		} else if version == "1.15.0" {
			code = strings.Replace(code, "Preview(source.Title, 1024)", "Preview(source.Title, 2048)", 1)
		} else if version == "1.4.0" {
			code = strings.Replace(code, `if !SourceFallbackAllowed(err) {`, `if false {`, 1)
		} else if version == "1.5.0" {
			code = strings.Replace(code, `if q.NumResults == 0 {`, `if true {`, 1)
		} else if version == "1.1.0" || version == "1.2.0" {
			value := "3"
			if version == "1.2.0" {
				value = "21"
			}
			code = strings.Replace(code, `q.NumResults = 5`, `q.NumResults = `+value, 1)
		}
		override := filepath.Join(t.TempDir(), filepath.Base(name))
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = override
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../../cmd/plugins/web-access").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err := os.ReadFile("../../../plugins/web-access/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if rsiplugin.Decode(raw, &manifest) != nil {
		t.Fatal("invalid manifest")
	}
	manifest.Version = version
	if version == "0.9.4" {
		manifest.Provides = manifest.Provides[:7]
	}
	if version == "0.9.0" {
		manifest.Provides = manifest.Provides[:2]
		manifest.Requests = []string{"resource.call"}
	}
	if version == "0.9.1" {
		manifest.Provides = manifest.Provides[:3]
		manifest.Requests = []string{"resource.call"}
	}
	if version == "0.9.2" {
		manifest.Provides = manifest.Provides[:5]
	}
	if version == "0.9.3" {
		manifest.Provides = manifest.Provides[:6]
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}
func selectWebVersion(t *testing.T, h rsi.Host, baseline, version string, count, index int) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, webVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "web-access", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "web-access", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy := webOwnerPolicy(count, index)
	if version == "0.9.4" {
		policy.AllowedContracts = policy.AllowedContracts[:7]
		policy.Cases = policy.Cases[:8]
	}
	if version == "1.25.0" {
		policy.Cases[9].Expected = json.RawMessage(`{"decoder":"html","error_code":""}`)
	}
	if version == "0.9.0" {
		policy.AllowedContracts = policy.AllowedContracts[:2]
		policy.Cases = policy.Cases[:2]
	}
	if version == "0.9.1" {
		policy.AllowedContracts = policy.AllowedContracts[:3]
		policy.Cases = policy.Cases[:4]
	}
	if version == "0.9.2" {
		policy.AllowedContracts = policy.AllowedContracts[:5]
		policy.Cases = policy.Cases[:6]
	}
	if version == "0.9.3" {
		policy.AllowedContracts = policy.AllowedContracts[:6]
		policy.Cases = policy.Cases[:7]
	}
	if version == "1.20.0" || version == "1.21.0" || version == "1.22.0" || version == "1.23.0" || version == "1.24.0" {
		policy.Cases[7] = webImageOwnerCase(version)
	}
	if version == "1.14.0" {
		policy.Cases[4].Expected, _ = json.Marshal(webaccess.FetchPlan{TextBytes: len("fetch fixture") + (1 << 20)})
	}
	if version == "1.15.0" {
		var value webaccess.NormalizePlan
		_ = json.Unmarshal(policy.Cases[5].Expected, &value)
		value.LimitBytes = 30000
		policy.Cases[5].Expected, _ = json.Marshal(value)
	}
	if version == "1.17.0" {
		var value webaccess.NormalizePlan
		_ = json.Unmarshal(policy.Cases[5].Expected, &value)
		value.Rows[0].Index += 99
		policy.Cases[5].Expected, _ = json.Marshal(value)
	}
	if version == "1.10.0" {
		policy.Cases[2].Resources[0].Input = json.RawMessage(`{"slot":0,"query":-1,"source":99}`)
	}
	policy.ID += "-" + version
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "web-search-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

type webRoundTrip func(*http.Request) (*http.Response, error)

func (f webRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebPluginSearchActualTaskUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	r.ConfigureWebAccess(cfg, nil)
	counts := []int{}
	client := &http.Client{Transport: webRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "search.example" || request.URL.Query().Get("q") != "public query" || request.Header.Get("Authorization") != "" {
			t.Error("original public query/resource scope changed", request.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com/evidence","title":"actual source","content":"original public evidence"}]}`)), Request: request}, nil
	})}
	r.webSourceRead = func(c context.Context, source string, q webaccess.SearchQuery, hosted webaccess.QuerySearch) (webaccess.SourceObservation, error) {
		counts = append(counts, q.NumResults)
		return webaccess.ReadSource(c, cfg, source, q, hosted, nil, client)
	}
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	args := `{"query":"public query"}`
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "source observed"}, nil
		}
		return calls(webaccess.SearchName, args), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Search the requested public evidence")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id string, want int, managed bool) tool.Result {
		t.Helper()
		before := len(counts)
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.SearchName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil {
			t.Fatal(raw)
		}
		var value struct {
			webaccess.Document
			ResponseID string `json:"responseId"`
		}
		if json.Unmarshal([]byte(result.Text), &value) != nil || len(value.Queries) != 1 || value.Queries[0].Query != "public query" || value.Queries[0].Provider != "searxng" || len(value.Queries[0].Sources) != 1 || value.Queries[0].Sources[0].URL != "https://example.com/evidence" || !strings.Contains(value.Queries[0].Text, "original public evidence") || value.ResponseID == "" {
			t.Fatal(result.Text)
		}
		if len(counts) != before+1 || counts[len(counts)-1] != want {
			t.Fatal("versioned source default was not dispatched", counts, before, want)
		}
		flag := scalar(t, s, `SELECT count(*) FROM rsi_run b,json_each(b.managed_services_json) j WHERE run_id=? AND j.value=?`, id, webaccess.SearchContract)
		if managed != (flag == 1) {
			t.Fatal("migration boundary drift", flag, managed)
		}
		return result
	}
	native := queue("web-native")
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	if len(counts) != 0 {
		t.Fatal("candidate verification invoked production search resources", counts)
	}
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check(native, 5, false)
	old := queue("web-retained-old")
	second, pkg := selectWebVersion(t, h, first.Revision, "1.1.0", 3, 0)
	oldResult := check(old, 5, true)
	check(queue("web-three"), 3, true)
	args = `{"query":"public query","numResults":4}`
	check(queue("web-explicit-count"), 4, true)
	args = `{"query":"public query"}`
	retained := queue("web-retained-three")
	activateDelegation(t, h, "web-rollback", second, first.Revision, "web-search-owner")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(retained, 3, true)
	check(queue("web-restored-five"), 5, true)
	// Original published bytes remain readable after package retirement/rollback.
	var scope artifact.Scope
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { var e error; scope, e = runScope(ctx, tx, old); return e }); err != nil {
		t.Fatal(err)
	}
	if len(oldResult.ArtifactRefs) != 1 {
		t.Fatal(oldResult)
	}
	reader, err := s.Artifacts().Open(ctx, scope, oldResult.ArtifactRefs[0])
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !strings.Contains(string(body), "original public evidence") {
		t.Fatal("published evidence changed", err)
	}
	if _, err = h.Store.Deactivate(ctx, "web-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	before := len(counts)
	disabled := queue("web-disabled")
	mustDrain(t, a)
	if len(counts) != before || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='failed' AND json_extract(error_json,'$.code')='PROVIDER_UNAVAILABLE'`, disabled, webaccess.SearchName) != 1 {
		t.Fatal("deactivation resurrected native search", counts)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='web-access'`) != 0 {
		t.Fatal("source port added another opaque effect ledger")
	}
}

func TestWebPluginSearchHostRefusesAcceptedMaliciousPlansAndNeverRepeatsSource(t *testing.T) {
	for _, version := range []string{"1.2.0", "1.3.0", "1.4.0", "1.5.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
			args := `{"query":"public query"}`
			if version == "1.4.0" {
				cfg.SourceOrder = append(cfg.SourceOrder, "tavily")
				cfg.TavilySecretRef = "private-unused-tavily-ref"
			}
			if version == "1.5.0" {
				args = `{"query":"public query","numResults":4}`
			}
			calls := []llm.ToolCall{{ID: "original-search", Name: webaccess.SearchName, Arguments: args}}
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				initial, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				pkg, err := h.Install(ctx, webVersionPackage(t, version))
				if err != nil {
					t.Fatal(err)
				}
				graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "web-access", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "web-access", StateVersion: 1}}})
				if err != nil {
					t.Fatal(err)
				}
				count, index := 5, 0
				if version == "1.2.0" {
					count = 21
				}
				if version == "1.3.0" {
					index = 99
				}
				policy := webOwnerPolicy(count, index)
				if version == "0.9.4" {
					policy.AllowedContracts = policy.AllowedContracts[:7]
					policy.Cases = policy.Cases[:8]
				}
				if version == "1.25.0" {
					policy.Cases[9].Expected = json.RawMessage(`{"decoder":"html","error_code":""}`)
				}
				policy.ID = "malicious-web-" + version
				if version == "1.4.0" {
					var input webaccess.SearchRequest
					_ = json.Unmarshal(policy.Cases[0].Input, &input)
					input.Sources = append(input.Sources, "tavily")
					policy.Cases[0].Input, _ = json.Marshal(input)
					extra := policy.Cases[0].Resources[0]
					extra.Key = "tavily"
					extra.Input = json.RawMessage(`{"provider":"tavily","num_results":5}`)
					policy.Cases[0].Resources[0].Output = json.RawMessage(`{"text":"","sources":[],"retrieved_at":"","error_code":"AUTH_REQUIRED"}`)
					policy.Cases[0].Resources = append(policy.Cases[0].Resources, extra)
					var plan webaccess.SearchPlan
					_ = json.Unmarshal(policy.Cases[0].Expected, &plan)
					plan.Provider = "tavily"
					policy.Cases[0].Expected, _ = json.Marshal(plan)
				}
				if err = h.Store.PutPolicy(ctx, policy); err != nil {
					t.Fatal(err)
				}
				activateDelegation(t, h, "bad-web-"+version, initial.Revision, graph.Revision, policy.ID)
			})
			defer r.Release(ctx, claim)
			sources := []string{}
			client := &http.Client{Transport: webRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Query().Get("q") != "public query" {
					t.Error("query was replaced", request.URL)
				}
				status := 200
				if version == "1.4.0" {
					status = 401
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"results":[{"url":"https://example.com/evidence","title":"actual source","content":"original source"}]}`)), Request: request}, nil
			})}
			r.webSourceRead = func(c context.Context, source string, q webaccess.SearchQuery, hosted webaccess.QuerySearch) (webaccess.SourceObservation, error) {
				sources = append(sources, source)
				return webaccess.ReadSource(c, cfg, source, q, hosted, nil, client)
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error == nil {
				t.Fatal(results, err)
			}
			wantCalls, code := 0, "PROVIDER_UNAVAILABLE"
			if version == "1.3.0" || version == "1.4.0" {
				wantCalls, code = 1, "UNKNOWN_SIDE_EFFECT"
			}
			if len(sources) != wantCalls || results[0].Error.Code != code || (len(sources) > 0 && sources[0] != "searxng") {
				t.Fatal("Host floor or source intent violated", sources, results[0].Error)
			}
			again, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(again) != 1 || again[0].Error == nil || again[0].Error.Code != code || len(sources) != wantCalls {
				t.Fatal("failed/unknown source request was replayed", again, sources, err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND attempt_count=1`, webaccess.SearchName) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal("resource broker replaced the original effect ledger")
			}
		})
	}
}

func TestWebPluginSearchLateForgetAndGuardianCannotReleaseSourceBytes(t *testing.T) {
	for _, mode := range []string{"forget", "stop"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{SourceOrder: []string{"searxng"}, SearXNGURL: "https://search.example"}
			calls := []llm.ToolCall{{ID: "private-source", Name: webaccess.SearchName, Arguments: `{"query":"public query"}`}}
			var h rsi.Host
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
			})
			defer r.Release(ctx, claim)
			owner := idAgent(t, s)
			count := 0
			r.webSourceRead = func(context.Context, string, webaccess.SearchQuery, webaccess.QuerySearch) (webaccess.SourceObservation, error) {
				count++
				if mode == "forget" {
					if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
						t.Fatal("resource held a writer or blocked Forget", err)
					}
				} else if err := h.Store.Stop(ctx, true); err != nil {
					t.Fatal(err)
				}
				return webaccess.SourceObservation{Text: "PRIVATE_LATE_SOURCE_BYTES", Sources: []llm.WebSource{{URL: "https://example.com/private-source", Title: "PRIVATE_LATE_TITLE"}}, RetrievedAt: "2026-11-09T00:00:00Z"}, nil
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if count != 1 {
				t.Fatal(count, err)
			}
			if mode == "forget" {
				if !errors.Is(err, agent.ErrClaimLost) {
					t.Fatal("privacy loss was hidden", results, err)
				}
			} else {
				if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" {
					t.Fatal(results, err)
				}
				if err = h.Store.Stop(ctx, false); err != nil {
					t.Fatal(err)
				}
				if _, err = executor.Execute(ctx, exchange, calls); err != nil || count != 1 {
					t.Fatal("resume replayed an uncertain source", count, err)
				}
			}
			if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_LATE')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal("late source bytes acquired published authority")
			}
		})
	}
}

func TestWebPluginSearchDefaultPriorityIsVersionedButExplicitOwnerOrderIsFixed(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "default"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{TavilySecretRef: "host-only-credential-ref"}
			if explicit {
				cfg.SourceOrder = []string{"native", "tavily"}
			}
			calls := []llm.ToolCall{{ID: "ordered-source", Name: webaccess.SearchName, Arguments: `{"query":"public query"}`}}
			_, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				selected, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectWebVersion(t, h, selected.Revision, "1.6.0", 5, 0)
			})
			defer r.Release(ctx, claim)
			observed := []string{}
			r.webSourceRead = func(_ context.Context, provider string, q webaccess.SearchQuery, _ webaccess.QuerySearch) (webaccess.SourceObservation, error) {
				observed = append(observed, provider)
				if q.Query != "public query" {
					t.Fatal(q)
				}
				if provider == "native" {
					return webaccess.SourceObservation{}, llm.ModelIncompatible
				}
				return webaccess.SourceObservation{Text: "original source", Sources: []llm.WebSource{{URL: "https://example.com/evidence", Title: "actual source"}}, RetrievedAt: "2026-11-09T00:00:00Z"}, nil
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error != nil {
				t.Fatal(results, err)
			}
			want := []string{"tavily"}
			if explicit {
				want = []string{"native", "tavily"}
			}
			if strings.Join(observed, ",") != strings.Join(want, ",") {
				t.Fatal("default priority was fixed in Host or explicit intent was overridden", observed, want)
			}
		})
	}
}
