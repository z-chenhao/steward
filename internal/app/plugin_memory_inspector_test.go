package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestMemoryInspectorActualOwnerPageGraphPinDisableAndFirstColdReference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, e := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if e != nil {
		t.Fatal(e)
	}
	cfg.Defaults = &config.Defaults{}
	cfg.Memory = &memory.Config{EncryptionKeyRef: "credential:memory-test-key"}
	secrets := secretmemory.New()
	const token = "synthetic-memory-inspector-owner-token"
	for key, value := range map[string]string{"credential:memory-test-key": strings.Repeat("a", 32), "credential:memory-test-owner": token} {
		if e = secrets.Put(ctx, key, []byte(value)); e != nil {
			t.Fatal(e)
		}
	}
	a, e := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	if e = a.prepareCore(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = a.BindChannel(ctx, "web", "browser", "local", true, 0); e != nil {
		t.Fatal(e)
	}
	written, e := a.ManageMemory(ctx, "original-inspector-note", memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Original managed inspector note", Content: "Original authorized private content"})
	if e != nil {
		t.Fatal(e)
	}
	ref, ok := written["ref"].(string)
	if !ok || ref == "" {
		t.Fatal(written)
	}
	request := func(path string) (int, string) {
		t.Helper()
		server := &web.Server{Config: web.Config{TokenRef: "credential:memory-test-owner"}, Core: a, Secrets: secrets, Memory: a}
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		server.ServeHTTP(out, req)
		return out.Code, out.Body.String()
	}
	detail := "/developer/memory?ref=" + url.QueryEscape(ref)
	if code, body := request(detail); code != http.StatusOK || !strings.Contains(body, "Original authorized private content") || strings.Contains(body, `name="inspector_revision"`) {
		t.Fatal("original native detail", code, body)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	configure := func() rsi.Host {
		host := a.Plugins()
		host.Catalog.Launcher = carrier
		a.runtimeRuns().ConfigurePlugins(host.Catalog)
		return host
	}
	host := configure()
	originalPackage := t.TempDir()
	if out, e := exec.Command("python3", "../../tests/fixtures/plugins/memory-inspector-original-v1/build.py", originalPackage).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	pkg, e := host.Install(ctx, originalPackage)
	if e != nil {
		t.Fatal(e)
	}
	graph, e := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "memory-inspector", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "memory-inspector", StateVersion: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	expected := json.RawMessage(`{"urls":[],"labels":{"source":{"conversation":"Source message","task":"Source task","tool":"memory_manage","control":"Local control","other":"Source"},"origin":{"written":"Written note","dream":"Dreaming","original":"Original record","automatic":"Automatically extracted","explicit":"Explicitly remembered","other":"Unknown origin"},"layer":{"transcripts":"Transcripts","representations":"Memory representations","injected":"Injected context","notes":"Memory notes"},"default_status":"active"}}`)
	view, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/managed-memory-v1.html.txt")
	if err != nil {
		t.Fatal(err)
	}
	var original contract.Result
	if err = rsiplugin.Decode(expected, &original); err != nil {
		t.Fatal(err)
	}
	original.View = contract.View{Resource: contract.ViewResource, Digest: rsiplugin.FileDigest(view), Source: string(view)}
	styles, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/memory.css.txt")
	if err != nil {
		t.Fatal(err)
	}
	original.Stylesheet = contract.Stylesheet{Resource: contract.StylesheetResource, Digest: rsiplugin.FileDigest(styles), Source: string(styles)}
	copy, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	original.Localization = contract.Localization{Resource: contract.LocalizationResource, Digest: rsiplugin.FileDigest(copy), Source: string(copy)}
	program, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/worker-v1.js.txt")
	if err != nil {
		t.Fatal(err)
	}
	original.Program = contract.BrowserProgram{Resource: contract.ProgramResource, Digest: rsiplugin.FileDigest(program), Source: string(program)}
	expected, err = json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	policy := rsi.Policy{ID: "original-memory-inspector-page", AllowedContracts: []string{contract.Presentation}, Cases: []rsi.Case{{InstanceID: "memory-inspector", Contract: contract.Presentation, Input: json.RawMessage(`{"navigation":[]}`), Expected: expected}}}
	if e = host.Store.PutPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	status, e := host.Store.Status(ctx)
	if e != nil {
		t.Fatal(e)
	}
	candidate := rsi.Candidate{ID: "managed-memory-inspector-page", Baseline: status.Revision, Target: graph.Revision, PolicyID: policy.ID, Goal: "Preserve original private owner memory view"}
	if e = host.Propose(ctx, candidate); e != nil {
		t.Fatal(e)
	}
	decision, e := host.Verify(ctx, candidate.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = host.Activate(ctx, rsi.Activation{OperationID: candidate.ID, Expected: status.Revision, Target: graph.Revision, DecisionID: decision}); e != nil {
		t.Fatal(e)
	}
	if code, body := request(detail); code != http.StatusOK || !strings.Contains(body, "Original authorized private content") || !strings.Contains(body, "inspector_revision="+url.QueryEscape(graph.Revision)) {
		t.Fatal("actual managed owner detail did not pin navigation", code, body)
	}
	if code, body := request("/developer/memory"); code != http.StatusOK || !strings.Contains(body, `name="inspector_revision" value="`+graph.Revision+`"`) {
		t.Fatal("managed filters lost frozen graph", code, body)
	}
	stylePath := "/api/developer/memory/style?inspector_revision=" + url.QueryEscape(graph.Revision)
	if code, body := request(stylePath); code != http.StatusOK || body != string(styles) {
		t.Fatal("actual frozen stylesheet did not match original", code)
	}
	if os.Getenv("STEWARD_TEST_APP_BROWSER") == "1" {
		checkMemoryInspectorManagedBrowser(t, &web.Server{Config: web.Config{TokenRef: "credential:memory-test-owner"}, Core: a, Secrets: secrets, Memory: a}, token, graph.Revision, ref, original.Stylesheet.Digest, "记忆详情", "24px", "baseline")
	}
	iterationSource, iteration := memoryInspectorIteration(t, original)
	iteratedPackage, e := host.Install(ctx, iterationSource)
	if e != nil {
		t.Fatal(e)
	}
	iteratedGraph, e := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "memory-inspector", PackageDigest: iteratedPackage.Digest, Config: json.RawMessage(`{}`), StateNamespace: "memory-inspector", StateVersion: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	iterationExpected, e := json.Marshal(iteration)
	if e != nil {
		t.Fatal(e)
	}
	iterationPolicy := rsi.Policy{ID: "independent-memory-inspector-page", AllowedContracts: []string{contract.Presentation}, Cases: []rsi.Case{{InstanceID: "memory-inspector", Contract: contract.Presentation, Input: json.RawMessage(`{"navigation":[]}`), Expected: iterationExpected}}}
	if e = host.Store.PutPolicy(ctx, iterationPolicy); e != nil {
		t.Fatal(e)
	}
	selectVersion := func(id, target, policyID string) {
		t.Helper()
		status, e := host.Store.Status(ctx)
		if e != nil {
			t.Fatal(e)
		}
		candidate := rsi.Candidate{ID: id, Baseline: status.Revision, Target: target, PolicyID: policyID, Goal: "Independently iterate inspector presentation without changing facts"}
		if e = host.Propose(ctx, candidate); e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: status.Revision, Target: target, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	selectVersion("upgrade-memory-inspector", iteratedGraph.Revision, iterationPolicy.ID)
	if code, body := request(detail); code != http.StatusOK || !strings.Contains(body, "独立版本详情") || !strings.Contains(body, "Original authorized private content") {
		t.Fatal("independent copy did not reach real owner page", code)
	}
	if code, body := request(detail + "&inspector_revision=" + url.QueryEscape(graph.Revision)); code != http.StatusOK || strings.Contains(body, "独立版本详情") || !strings.Contains(body, "记忆详情") {
		t.Fatal("upgrade changed published original view reference", code)
	}
	iteratedStylePath := "/api/developer/memory/style?inspector_revision=" + url.QueryEscape(iteratedGraph.Revision)
	if code, body := request(iteratedStylePath); code != http.StatusOK || body != iteration.Stylesheet.Source {
		t.Fatal("independent exact stylesheet did not reach route", code)
	}
	if code, body := request(stylePath); code != http.StatusOK || body != original.Stylesheet.Source {
		t.Fatal("upgrade changed retained original styles", code)
	}
	if os.Getenv("STEWARD_TEST_APP_BROWSER") == "1" {
		checkMemoryInspectorManagedBrowser(t, &web.Server{Config: web.Config{TokenRef: "credential:memory-test-owner"}, Core: a, Secrets: secrets, Memory: a}, token, iteratedGraph.Revision, ref, iteration.Stylesheet.Digest, "独立版本详情", "32px", "upgrade")
	}
	selectVersion("rollback-memory-inspector", graph.Revision, policy.ID)
	if code, body := request(detail); code != http.StatusOK || strings.Contains(body, "独立版本详情") || !strings.Contains(body, "记忆详情") {
		t.Fatal("rollback did not restore exact original copy", code)
	}
	if code, body := request(iteratedStylePath); code != http.StatusOK || body != iteration.Stylesheet.Source {
		t.Fatal("rollback lost newer frozen source", code)
	}
	if code, body := request(detail + "&inspector_revision=" + url.QueryEscape(iteratedGraph.Revision)); code != http.StatusOK || !strings.Contains(body, "独立版本详情") {
		t.Fatal("rollback lost newer published view reference", code)
	}
	if _, e = host.Store.Deactivate(ctx, "disable-memory-inspector-page", graph.Revision); e != nil {
		t.Fatal(e)
	}
	if code, _ := request(detail); code != http.StatusServiceUnavailable {
		t.Fatal("disabled managed view fell back to native", code)
	}
	if code, _ := request("/api/developer/memory?ref=" + url.QueryEscape(ref)); code != http.StatusOK {
		t.Fatal("view disable changed authoritative read-only API", code)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	a.eventsRequired.Store(false)
	configure()
	// First read after reopen: no graph selection, reload or repair is performed.
	if code, body := request(detail + "&inspector_revision=" + graph.Revision); code != http.StatusOK || !strings.Contains(body, "Original authorized private content") {
		t.Fatal("first cold retained view failed", code, body)
	}
	if code, _ := request(detail); code != http.StatusServiceUnavailable {
		t.Fatal("cold disable lost durable boundary", code)
	}
	if code, body := request(stylePath); code != http.StatusOK || body != string(styles) {
		t.Fatal("retained stylesheet lost after cold disabled page read", code)
	}
	// Independently restart again: the newer retained graph must be the first
	// interaction too, without using the older graph as a preparation/repair step.
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	a.eventsRequired.Store(false)
	configure()
	if code, body := request(detail + "&inspector_revision=" + url.QueryEscape(iteratedGraph.Revision)); code != http.StatusOK || !strings.Contains(body, "独立版本详情") || !strings.Contains(body, "Original authorized private content") {
		t.Fatal("first cold newer retained view lost original facts/version", code)
	}
	if code, body := request(iteratedStylePath); code != http.StatusOK || body != iteration.Stylesheet.Source {
		t.Fatal("newer cold retained stylesheet changed", code)
	}
	if code, _ := request(detail); code != http.StatusServiceUnavailable {
		t.Fatal("newer reference silently reactivated default", code)
	}

}
