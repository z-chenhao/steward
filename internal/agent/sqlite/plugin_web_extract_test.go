package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/plugin/webextract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func extractModule(t *testing.T, version string) []byte {
	t.Helper()
	source, err := os.ReadFile("../../plugin/webextract/extract.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if version == "1.4.0" {
		source = bytes.Replace(source, []byte("title = article?.title || document.title || '';"), []byte("title = 'WRONG_MODULE';"), 1)
	}
	if version == "1.1.0" {
		source = bytes.Replace(source, []byte("## Page ${index + 1}"), []byte("## Sheet ${index + 1}"), 1)
	}
	directory := t.TempDir()
	entry := filepath.Join(directory, "recipe.mjs")
	if err = os.WriteFile(entry, source, 0600); err != nil {
		t.Fatal(err)
	}
	dependencies, _ := filepath.Abs("../../../runtime/javascript/node_modules")
	source, err = parserbundle.Bundle(context.Background(), entry, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	return source
}
func extractPackage(t *testing.T, version string, module []byte) string {
	t.Helper()
	directory := t.TempDir()
	source, err := os.ReadFile("../../plugin/webextract/service.go")
	if err != nil {
		t.Fatal(err)
	}
	original, err := filepath.Abs("../../plugin/webextract/service.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(source)
	if version == "1.2.0" {
		code = strings.Replace(code, "var metadata Metadata", `request.URL="https://forged.example/";var metadata Metadata`, 1)
	}
	if version == "1.3.0" {
		code = strings.Replace(code, "plan.Digest = rsiplugin.FileDigest(raw)", `plan.Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000";_ = raw`, 1)
	}
	file := filepath.Join(t.TempDir(), "service.go")
	if err = os.WriteFile(file, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: file}})
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(path, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", path, "-o", filepath.Join(directory, "plugin"), "../../../cmd/plugins/web-extract").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err := os.ReadFile("../../../plugins/web-extract/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if rsiplugin.Decode(raw, &manifest) != nil {
		t.Fatal("manifest")
	}
	manifest.Version = version
	if err = os.WriteFile(filepath.Join(directory, webextract.ModuleFile), module, 0600); err != nil {
		t.Fatal(err)
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
func extractHTML() string {
	return `<html><head><title>Original extractor title</title></head><body><article><h1>Evidence</h1>` + strings.Repeat(`<p>Original source evidence and provenance stay intact.</p>`, 60) + `<a href="/ref">original reference</a></article></body></html>`
}
func extractPDF() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 12 Tf 20 100 Td (Original PDF body retained.) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	var out strings.Builder
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	start := out.Len()
	out.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", start)
	return []byte(out.String())
}
func extractPolicy(t *testing.T, cfg webaccess.Config, version string) rsi.Policy {
	t.Helper()
	policy := webOwnerPolicy(5, 0)
	policy.ID = "web-extract-owner-" + version
	policy.AllowedContracts = append(policy.AllowedContracts, webextract.Contract)
	policy.AllowedRequests = append(policy.AllowedRequests, "compute.web-extract")
	for _, item := range []struct {
		kind string
		data []byte
	}{{"text/html", []byte(extractHTML())}, {"application/pdf", extractPDF()}} {
		raw, _ := json.Marshal(map[string]any{"url": "https://example.com/source", "mediaType": item.kind, "data": item.data})
		command := exec.Command(cfg.ExtractCommand, cfg.ExtractScript)
		command.Stdin = bytes.NewReader(raw)
		output, err := command.Output()
		if err != nil {
			t.Fatal(string(output), err)
		}
		var response struct{ Title, Text string }
		if json.Unmarshal(output, &response) != nil || response.Text == "" {
			t.Fatal(string(output))
		}
		if version == "1.1.0" {
			response.Text = strings.ReplaceAll(response.Text, "## Page ", "## Sheet ")
		}
		expected, _ := json.Marshal(webextract.Plan{Metadata: webextract.Metadata{TitleBytes: len(response.Title), TextBytes: len(response.Text)}, Digest: rsiplugin.FileDigest([]byte(response.Title + response.Text))})
		input, _ := json.Marshal(webextract.Request{InputID: "document", URL: "https://example.com/source", MediaType: item.kind})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "web-extract", Contract: webextract.Contract, Input: input, Expected: expected, Inputs: []rsi.InputData{{ID: "document", Data: item.data}}})
	}
	return policy
}
func selectExtract(t *testing.T, h rsi.Host, cfg webaccess.Config, runtime, baseline, version string) (string, string, error) {
	t.Helper()
	return selectExtractSource(t, h, cfg, runtime, baseline, version, cfg.ScopeIdentity())
}

func selectExtractSource(t *testing.T, h rsi.Host, cfg webaccess.Config, runtime, baseline, version, sourceIdentity string) (string, string, error) {
	t.Helper()
	ctx := context.Background()
	current, err := h.Store.Graph(ctx, baseline)
	if err != nil {
		t.Fatal(err)
	}
	module := extractModule(t, version)
	if version == "1.6.0" {
		module, err = os.ReadFile(cfg.ExtractScript)
		if err != nil {
			t.Fatal(err)
		}
	}
	pkg, err := h.Install(ctx, extractPackage(t, version, module))
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(webextract.Config{RuntimeRevision: runtime, SourceIdentity: sourceIdentity})
	bindings := []rsiplugin.Binding{}
	for _, binding := range current.Graph.Bindings {
		if binding.InstanceID != "web-extract" {
			bindings = append(bindings, binding)
		}
	}
	bindings = append(bindings, rsiplugin.Binding{InstanceID: "web-extract", PackageDigest: pkg.Digest, Config: config, StateNamespace: "web-extract", StateVersion: 1})
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	policy := extractPolicy(t, cfg, version)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: policy.ID, Baseline: baseline, Target: graph.Revision, PolicyID: policy.ID, Goal: "verify actual sealed Node method and original scope"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, policy.ID)
	if err != nil {
		return graph.Revision, pkg.Digest, err
	}
	_, err = h.Activate(ctx, rsi.Activation{OperationID: policy.ID, Expected: baseline, Target: graph.Revision, DecisionID: decision})
	return graph.Revision, pkg.Digest, err
}
func extractConfig(t *testing.T) webaccess.Config {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	command := filepath.Join(directory, "original-node")
	in, err := os.Open(node)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(command, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(directory, "original-extractor.mjs")
	if err = os.WriteFile(script, extractModule(t, "1.0.0"), 0600); err != nil {
		t.Fatal(err)
	}
	return webaccess.Config{ExtractCommand: command, ExtractScript: script}
}

func TestWebPluginExtractActualHTMLPDFUpgradeRetainedResourcesRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
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
	first, _, err := selectExtract(t, h, cfg, resource.Digest(), status.Revision, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		fetches++
		kind, source := "text/html", []byte(extractHTML())
		if strings.HasSuffix(url, ".pdf") {
			kind, source = "application/pdf", extractPDF()
		}
		title, text, err := webcontent.ParseDocument(c, cfg.Extraction(), url, kind, source)
		return webaccess.Page{URL: url, MediaType: kind, Title: title, Text: text, RetrievedAt: "2026-11-09T00:00:00Z", Untrusted: true}, err
	}
	url := "https://example.com/source.pdf"
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "read complete"}, nil
		}
		args, _ := json.Marshal(map[string]string{"url": url})
		return calls(webaccess.ContentName, string(args)), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Read original public source").RunID }
	check := func(id, want string) {
		t.Helper()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.ContentName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var value webaccess.ResearchEnvelope
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &value) != nil || len(value.Pages) != 1 || !strings.Contains(value.Pages[0].Text, want) || value.Pages[0].RetrievedAt != "2026-11-09T00:00:00Z" || !value.Pages[0].Untrusted {
			t.Fatal(raw)
		}
		var saved webaccess.Document
		if json.Unmarshal([]byte(readArtifact(t, s, toolRunScope(t, s, id), value.ResponseID)), &saved) != nil || len(saved.Pages) != 1 || saved.Pages[0].Text != value.Pages[0].Text {
			t.Fatal("original published evidence lost")
		}
	}
	old := queue("extract-queued-original")
	second, pkg, err := selectExtract(t, h, cfg, resource.Digest(), first, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	check(old, "## Page 1")
	check(queue("extract-new-method"), "## Sheet 1")
	url = "https://example.com/source"
	check(queue("extract-html"), "original reference")
	// Remove the mutable installation. New and retained Run execution uses graph
	// resources; no policy reselect or fixture repair follows this replacement.
	if err = os.WriteFile(cfg.ExtractScript, []byte("replaced source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.ExtractCommand, []byte("replaced interpreter"), 0700); err != nil {
		t.Fatal(err)
	}
	retained := queue("extract-original-resources-after-replacement")
	activateDelegation(t, h, "extract-rollback", second, first, "web-extract-owner-1.0.0")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	url = "https://example.com/source.pdf"
	check(retained, "## Sheet 1")
	check(queue("extract-rollback-source"), "## Page 1")
	retained = queue("extract-before-disable")
	if _, err = h.Store.Deactivate(ctx, "extract-disable", first); err != nil {
		t.Fatal(err)
	}
	check(retained, "## Page 1")
	before := fetches
	disabled := queue("extract-disabled")
	mustDrain(t, a)
	if fetches != before || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND status='failed'`, disabled) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(fetches, before)
	}
}

func TestWebPluginExtractGuardianRejectsActualModuleAndForgedScopeOrProof(t *testing.T) {
	for _, version := range []string{"1.2.0", "1.3.0", "1.4.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			cfg := extractConfig(t)
			s, r, _, _ := childFixture(t, webaccess.Definitions(cfg)...)
			r.ConfigureWebAccess(cfg, nil)
			h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
			status, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			module, _ := os.ReadFile(cfg.ExtractScript)
			resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = selectExtract(t, h, cfg, resource.Digest(), status.Revision, version)
			if !errors.Is(err, rsi.ErrVerification) {
				t.Fatal("invalid actual decoder passed Guardian", err)
			}
		})
	}
}

func TestWebPluginExtractLateForgetAndGuardianBeforePublication(t *testing.T) {
	for _, mode := range []string{"forget", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := extractConfig(t)
			calls := []llm.ToolCall{{ID: "private-extract", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/private.pdf"}`}}
			var h rsi.Host
			var revision string
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				status, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				module, _ := os.ReadFile(cfg.ExtractScript)
				resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
				if err != nil {
					t.Fatal(err)
				}
				revision, _, err = selectExtract(t, h, cfg, resource.Digest(), status.Revision, "1.0.0")
				if err != nil {
					t.Fatal(err)
				}
			})
			defer r.Release(ctx, claim)
			bound, bindErr := r.RegistryForRun(ctx, claim, executor.Registry)
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			executor.Registry = bound
			reached, fetches := 0, 0
			r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
				fetches++
				title, text, err := webcontent.ParseDocument(c, cfg.Extraction(), url, "application/pdf", extractPDF())
				return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
			}
			owner := idAgent(t, s)
			s.MainBoundary = func(point string) {
				if point != "after_web_extract_method" {
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
			if reached != 1 || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal(reached, fetches, results, err)
			}
		})
	}
}

func TestWebPluginExtractChildRetainsOriginalGraphAfterInstallationReplacement(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	r.ConfigureWebAccess(cfg, nil)
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	module, _ := os.ReadFile(cfg.ExtractScript)
	resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
	if err != nil {
		t.Fatal(err)
	}
	original, pkg, err := selectExtract(t, h, cfg, resource.Digest(), status.Revision, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		title, text, err := webcontent.ParseDocument(c, cfg.Extraction(), url, "application/pdf", extractPDF())
		return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
	}
	switched := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main delivery"}, nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Child extraction completed"}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"PRIVATE_PARENT_EXTRACTION"}`), nil
		}
		if last.Text == "read child pdf" {
			return calls(webaccess.ContentName, `{"url":"https://example.com/child.pdf"}`), nil
		}
		if !switched {
			if _, _, err := selectExtract(t, h, cfg, resource.Digest(), original, "1.1.0"); err != nil {
				t.Fatal(err)
			}
			if err := h.Store.Retire(ctx, pkg); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.ExtractScript, []byte("replacement source"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.ExtractCommand, []byte("replacement command"), 0700); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		return calls(agent.RunToolName, `{"task":"read child pdf"}`), nil
	}
	mustSubmit(t, a, "extract-child-original", "PRIVATE_MAIN_EXTRACTION")
	mustDrain(t, a)
	if !switched || fetches != 1 || scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.worker_position='child' AND c.status='completed' AND cb.graph_revision=? AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json`, original) != 1 {
		t.Fatal(switched, fetches)
	}
	var childID, raw string
	if err = s.db.QueryRow(`SELECT r.run_id,i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name=? AND i.status='succeeded'`, webaccess.ContentName).Scan(&childID, &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "## Page 1") || strings.Contains(raw, "## Sheet 1") {
		t.Fatal(raw)
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
		if bytes.Contains(raw, []byte("PRIVATE_PARENT_EXTRACTION")) || bytes.Contains(raw, []byte("PRIVATE_MAIN_EXTRACTION")) {
			t.Fatal("Child inherited private parent messages")
		}
	}
	if seen == 0 {
		t.Fatal("no real Child requests")
	}
}

func TestWebPluginExtractRealReleaseRecoveryDoesNotReplayOriginalNeverTool(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	calls := []llm.ToolCall{{ID: "recover-extract", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/recover.pdf"}`}}
	var h rsi.Host
	var original, runtime string
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		module, _ := os.ReadFile(cfg.ExtractScript)
		resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
		if err != nil {
			t.Fatal(err)
		}
		runtime = resource.Digest()
		original, _, err = selectExtract(t, h, cfg, runtime, status.Revision, "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
	})
	bound, err := r.RegistryForRun(ctx, claim, executor.Registry)
	if err != nil {
		t.Fatal(err)
	}
	executor.Registry = bound
	stop := keepWebReadClaim(r, claim)
	defer stop()
	fetches, reached := 0, 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		title, text, err := webcontent.ParseDocument(c, cfg.Extraction(), url, "application/pdf", extractPDF())
		return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
	}
	s.MainBoundary = func(point string) {
		if point != "after_web_extract_method" {
			return
		}
		s.MainBoundary = nil
		reached++
		stop()
		if err := r.Release(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = executor.Execute(ctx, exchange, calls); !errors.Is(err, agent.ErrClaimLost) || reached != 1 || fetches != 1 {
		t.Fatal(reached, fetches, err)
	}
	if _, _, err = selectExtract(t, h, cfg, runtime, original, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if next, err := r.Next(ctx); err != nil || next != claim.RunID() {
		t.Fatal(next, err)
	}
	next, err := r.Claim(ctx, claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	executor.Store = r.ToolStore(next)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(results, fetches, err)
	}
}

func TestWebPluginExtractCustomAbsoluteCommandAndScriptKeepFullInputAndLargeResult(t *testing.T) {
	ctx := context.Background()
	cfg := extractConfig(t)
	custom := []byte(`let raw='';for await(const chunk of process.stdin)raw+=chunk;const input=JSON.parse(raw);const count=Buffer.from(input.data,'base64').length;if(input.url.endsWith('/full')&&count!==8*1024*1024)throw Error('source shrank');process.stdout.write(JSON.stringify({title:'Owner custom parser',text:String(count)+' original bytes\n'+'x'.repeat(3*1024*1024)}));`)
	if err := os.WriteFile(cfg.ExtractScript, custom, 0600); err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{{ID: "custom-extract", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/full"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, custom)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = selectExtract(t, h, cfg, resource.Digest(), status.Revision, "1.6.0"); err != nil {
			t.Fatal(err)
		}
	})
	defer r.Release(ctx, claim)
	defer keepWebReadClaim(r, claim)()
	bound, err := r.RegistryForRun(ctx, claim, executor.Registry)
	if err != nil {
		t.Fatal(err)
	}
	executor.Registry = bound
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		title, text, err := webcontent.ParseDocument(c, cfg.Extraction(), url, "text/html", bytes.Repeat([]byte("x"), 8<<20))
		return webaccess.Page{URL: url, Title: title, Text: text, Untrusted: true}, err
	}
	if err = os.WriteFile(cfg.ExtractCommand, []byte("replacement"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.ExtractScript, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		t.Fatal(results, err)
	}
	var envelope webaccess.ResearchEnvelope
	if json.Unmarshal([]byte(results[0].Text), &envelope) != nil || len(envelope.Pages) != 1 || envelope.Pages[0].Title != "Owner custom parser" {
		t.Fatal(results[0].Text)
	}
	var document webaccess.Document
	body := readArtifact(t, s, toolRunScope(t, s, claim.RunID()), envelope.ResponseID)
	if len(body) <= 3<<20 || json.Unmarshal([]byte(body), &document) != nil || len(document.Pages) != 1 || !strings.HasPrefix(document.Pages[0].Text, "8388608 original bytes\n") || len(document.Pages[0].Text) <= 3<<20 {
		t.Fatal("custom input/output was truncated", len(body))
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("pure extraction added another operation journal")
	}
}

func TestWebPluginExtractMissingCatalogAndChangedConfigCannotUseRetainedAuthority(t *testing.T) {
	for _, mode := range []string{"missing-catalog", "changed-config", "changed-config-with-parser-scope"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := extractConfig(t)
			calls := []llm.ToolCall{{ID: "extract-authority", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/authority.pdf"}`}}
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				status, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				module, _ := os.ReadFile(cfg.ExtractScript)
				resource, err := h.CaptureWebParser(ctx, cfg.ExtractCommand, module)
				if err != nil {
					t.Fatal(err)
				}
				scope := cfg.ScopeIdentity()
				if mode == "changed-config-with-parser-scope" {
					scope = cfg.ParserSourceIdentity()
				}
				if _, _, err = selectExtractSource(t, h, cfg, resource.Digest(), status.Revision, "1.0.0", scope); err != nil {
					t.Fatal(err)
				}
			})
			defer r.Release(ctx, claim)
			bound, err := r.RegistryForRun(ctx, claim, executor.Registry)
			if err != nil {
				t.Fatal(err)
			}
			executor.Registry = bound
			fetches := 0
			r.webContentFetch = func(context.Context, webcontent.Config, string, string) (webaccess.Page, error) {
				fetches++
				return webaccess.Page{Text: "UNAUTHORIZED_BODY"}, nil
			}
			want := "PROVIDER_UNAVAILABLE"
			if mode == "missing-catalog" {
				r.plugins = nil
				executor.Store = r.ToolStore(claim)
				want = "PROVIDER_UNAVAILABLE"
			} else {
				r.webAccess.SearXNGURL = "https://changed.example/"
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != want || fetches != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
				raw, _ := json.Marshal(results)
				t.Fatal(mode, string(raw), fetches, err)
			}
		})
	}
}
