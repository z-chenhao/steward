package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func workbenchOwnerCase(t *testing.T) rsi.Case {
	t.Helper()
	raw, err := os.ReadFile("../rsi/builtin/web-workbench-owner-v1.0.6.json")
	if err != nil {
		t.Fatal(err)
	}
	var c rsi.Case
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func independentWorkbenchPackage(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../plugin/webworkbench/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.6"`, `Version: "1.1.0"`, 1)
	code = strings.Replace(code, `"encoding/json"`, `"encoding/json"`+"\n\"strings\"", 1)
	code = strings.Replace(code, `asset(copyText)`, `asset(strings.Replace(copyText, `+"`\"en\": \"Execution records\"`"+`, `+"`\"en\": \"Execution records v2\"`"+`, 1))`, 1)
	code = strings.Replace(code, `return contract.Presentation{View:`, `policy.PollMS=1200
    for i:=range policy.Live {if policy.Live[i].Kind=="tasks" {policy.Live[i].Slots=[]string{"ongoing-work"}}}
    return contract.Presentation{View:`, 1)
	code = strings.Replace(code, `asset(styles)`, `asset(strings.Replace(styles,"font-size:15px;font-weight:550","font-size:17px;font-weight:550",1))`, 1)

	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/web-workbench").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/web-workbench/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}
func TestWebWorkbenchActualHTTPIndependentCopyRollbackRetainedAndCold(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	if err = secrets.Put(ctx, "credential:workbench-test", []byte("private-workbench-token-at-least-24")); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	h := a.Plugins()
	h.Catalog.Launcher = carrier
	if _, err = a.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	install := func(source string) rsi.Package {
		t.Helper()
		p, err := h.Install(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	prepare := func(pkg rsi.Package) string {
		t.Helper()
		g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "web-workbench", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "web-workbench", StateVersion: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return g.Revision
	}
	owner := rsi.Policy{ID: "workbench-owner-original", AllowedContracts: []string{contract.Service}, Cases: []rsi.Case{workbenchOwnerCase(t)}}
	if err = h.Store.PutPolicy(ctx, owner); err != nil {
		t.Fatal(err)
	}
	activate := func(id, baseline, target, policy string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent workbench display iteration"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	original := prepare(install(defaultFeaturePackage(t, "web-workbench", nil)))
	activate("workbench-first", "", original, owner.ID)
	get := func(reference string, code int, variant bool) {
		t.Helper()
		server := web.Server{Config: web.Config{TokenRef: "credential:workbench-test"}, Core: a, Tasks: a.store.TaskReader(), Secrets: secrets, WebWorkbench: func(ctx context.Context, ref string) (contract.Presentation, string, bool, error) {
			return a.readWebWorkbenchWithHost(ctx, h, ref)
		}}
		paths := []string{"/tasks"}
		if reference != "" {
			paths = append(paths, "/api/updates?view=/tasks", "/api/workbench/style?workbench_revision="+reference)
		}
		for _, path := range paths {
			req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
			req.Header.Set("Authorization", "Bearer private-workbench-token-at-least-24")
			if reference != "" {
				req.Header.Set("X-Steward-Workbench-Reader", reference)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			if w.Code != code {
				t.Fatal("actual managed HTTP status", w.Code, w.Body.String())
			}

			body := w.Body.String()
			if code == 200 && strings.HasPrefix(path, "/api/workbench/style") {
				if strings.Contains(body, "font-size:17px;font-weight:550") != variant || w.Header().Get("X-Steward-Workbench-Reader") != reference {
					t.Fatal("stylesheet lost original graph", w.Code, w.Header())
				}
				continue
			}
			if code == 200 && strings.HasPrefix(path, "/api/updates") {
				var frame struct{ Regions map[string]string }
				if err := json.Unmarshal(w.Body.Bytes(), &frame); err != nil {
					t.Fatal(err)
				}
				_, statusPresent := frame.Regions["workspace-status"]
				if statusPresent == variant {
					t.Fatal("updates did not use selected region policy", frame.Regions)
				}
			}
			if start := strings.Index(body, `<script type="application/json" id="web-workbench-config">`); start >= 0 {
				end := strings.Index(body[start:], `</script>`) + start
				if end < start {
					t.Fatal("unterminated Host configuration")
				}
				var config struct {
					Revision string
					Policy   contract.Policy
				}
				raw := strings.TrimPrefix(body[start:end], `<script type="application/json" id="web-workbench-config">`)
				if err := json.Unmarshal([]byte(raw), &config); err != nil {
					t.Fatal(err)
				}
				want := 1000
				if variant {
					want = 1200
				}
				if config.Policy.PollMS != want || reference != "" && config.Revision != reference {
					t.Fatal("client recipe lost selection", config)
				}
				body = body[:start] + body[end+len(`</script>`):]
			}
			if code == 200 && strings.Contains(body, "Execution records v2") != variant {
				t.Fatal("actual page did not use selected producer", w.Body.String())
			}
			if code != 200 && strings.Contains(w.Body.String(), "No matching tasks") {
				t.Fatal("unavailable producer restored native page")
			}
			if code == 200 && path != "/tasks" && w.Header().Get("X-Steward-Workbench-Reader") != reference {
				t.Fatal("updates lost original presentation fence", w.Header())
			}
		}

	}
	get("", 200, false)
	changed := install(independentWorkbenchPackage(t))
	next := prepare(changed)
	owner.ID = "workbench-owner-copy-v2"
	var expected contract.Presentation
	if err = json.Unmarshal(owner.Cases[0].Expected, &expected); err != nil {
		t.Fatal(err)
	}
	expected.Copy.Source = strings.Replace(expected.Copy.Source, `"en": "Execution records"`, `"en": "Execution records v2"`, 1)
	expected.Copy.Digest = rsiplugin.FileDigest([]byte(expected.Copy.Source))

	expected.Policy.PollMS = 1200
	for i := range expected.Policy.Live {
		if expected.Policy.Live[i].Kind == "tasks" {
			expected.Policy.Live[i].Slots = []string{"ongoing-work"}
		}
	}
	expected.Stylesheet.Source = strings.Replace(expected.Stylesheet.Source, "font-size:15px;font-weight:550", "font-size:17px;font-weight:550", 1)
	expected.Stylesheet.Digest = rsiplugin.FileDigest([]byte(expected.Stylesheet.Source))
	owner.Cases[0].Expected, _ = json.Marshal(expected)
	if err = h.Store.PutPolicy(ctx, owner); err != nil {
		t.Fatal(err)
	}
	activate("workbench-upgrade", original, next, owner.ID)
	get("", 200, true)
	get(original, 200, false)
	activate("workbench-rollback", next, original, "workbench-owner-original")
	if err = h.Store.Retire(ctx, changed.Digest); err != nil {
		t.Fatal(err)
	}
	get("", 200, false)
	get(next, 200, true)
	if _, err = h.Store.Deactivate(ctx, "workbench-disable", original); err != nil {
		t.Fatal(err)
	}
	get("", 503, false)
	get(original, 200, false)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	h = a.Plugins()
	h.Catalog.Launcher = carrier
	// First dependent operations after reopening: no graph activation or repair.
	get("", 503, false)
	get(next, 200, true)
	if state, err := h.Store.Status(ctx); err != nil || state.Revision != "" {
		t.Fatal("read implicitly repaired selection", state, err)
	}
}
