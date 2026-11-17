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
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The actual default and independently changed client/source fixtures.
func browserInterfacePackage(t *testing.T) string { return browserFixturePackage(t, "1.21.0", true) }
func browserFixturePackage(t *testing.T, version string, provided bool) string {
	t.Helper()
	if version == "1.21.0" && provided {
		return defaultFeaturePackage(t, "apps-interface", nil)
	}
	dir := t.TempDir()
	profile, err := filepath.Abs("../plugin/appsinterface/profile.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/profile.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.19.0"`, `Version: "`+version+`"`, 1)

	marker := "contract.LocalizedInterface: compileLocalizedProfile"
	if !strings.Contains(code, marker) {
		t.Fatal("original localized producer missing")
	}
	if provided {
		code = strings.Replace(code, marker, "contract.BrowserProfileInterface: compileBrowserProfile", 1)
		if version == "1.22.0" {
			code = strings.Replace(code, "contract.BrowserProfileInterface: compileBrowserProfile", "contract.BrowserProfileInterface: compileBrowserProfile, "+marker, 1)
		}
	} else {
		code = strings.Replace(code, marker+", ", "", 1)
	}

	replacement := filepath.Join(t.TempDir(), "profile.go")
	if err := os.WriteFile(replacement, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{profile: replacement}
	if version == "1.22.0" {
		// The dual-protocol negative fixture owns its healthy original /2
		// producer; current production code no longer carries that entry point.
		path, err := filepath.Abs("../plugin/appsinterface/localization.go")
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/localization.go.txt")
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "localization.go")
		if err := os.WriteFile(target, body, 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = target
	}
	if version == "1.22.0" || version == "1.23.0" {
		path, err := filepath.Abs("../plugin/appsinterface/browser.go")
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modified := strings.Replace(string(body), "Worker: browserWorker", `Worker: ""`, 1)
		if version == "1.23.0" {
			modified = strings.Replace(string(body), "CSS: browserCSS", `CSS: browserCSS+"\n.app-account{padding:24px 0}"`, 1)
		}
		if modified == string(body) {
			t.Fatal("browser source marker missing")
		}
		target := filepath.Join(t.TempDir(), "browser.go")
		if err := os.WriteFile(target, []byte(modified), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = target
	}
	raw, _ = json.Marshal(map[string]any{"Replace": replacements})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-interface").CombinedOutput(); err != nil {
		t.Fatal(string(result), err)
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version

	services := make([]rsiplugin.Service, 0, len(manifest.Provides)+1)
	for _, service := range manifest.Provides {
		if service.Contract == contract.LocalizedInterface {
			if !provided {
				continue
			}
			if version == "1.22.0" {
				services = append(services, service)
			}
			service.Contract = contract.BrowserProfileInterface
			var schema map[string]json.RawMessage
			if err := json.Unmarshal(service.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			var properties map[string]json.RawMessage
			if err := json.Unmarshal(schema["properties"], &properties); err != nil {
				t.Fatal(err)
			}
			properties["view"] = json.RawMessage(`{"type":"object","properties":{"worker":{"type":"string"},"css":{"type":"string"}},"required":["worker","css"],"additionalProperties":false}`)
			schema["properties"], _ = json.Marshal(properties)
			var required []string
			if err := json.Unmarshal(schema["required"], &required); err != nil {
				t.Fatal(err)
			}
			schema["required"], _ = json.Marshal(append(required, "view"))
			service.OutputSchema, _ = json.Marshal(schema)
		}
		services = append(services, service)
	}
	manifest.Provides = services

	copyInterfaceViews(t, dir, manifest)
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAppsBrowserActualSealedProducerRouteRollbackAndColdReference(t *testing.T) {
	for _, prior := range []bool{true, false} {
		name := "existing-old"
		if !prior {
			name = "fresh-old-install"
		}
		t.Run(name, func(t *testing.T) { testBrowserProfileLifecycle(t, prior) })
	}
}
func testBrowserProfileLifecycle(t *testing.T, prior bool) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-browser-owner-at-least24"
	if err := secrets.Put(ctx, "credential:browser-owner", []byte(token)); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	if result, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(result), err)
	}
	var host rsi.Host
	var server *web.Server
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		server = &web.Server{Config: web.Config{TokenRef: "credential:browser-owner"}, Core: a, Secrets: secrets, Interface: func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, reference, locale)
		}}
	}
	bind()
	old, policyID := prepareInterface(t, ctx, host, "1.19.0")
	baseline := ""
	if prior {
		activateInterface(t, ctx, host, "", old, policyID, "browser-original")
		baseline = old
	}
	pkg, err := host.Install(ctx, browserInterfacePackage(t))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-interface", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-interface", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "browser-owner"
	worker, err := os.ReadFile("../plugin/appsinterface/view/browser-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../plugin/appsinterface/view/apps.css")
	if err != nil {
		t.Fatal(err)
	}

	view := contract.BrowserView{Worker: string(worker), CSS: string(css)}
	for i, name := range policy.AllowedContracts {
		if name == contract.LocalizedInterface {
			policy.AllowedContracts[i] = contract.BrowserProfileInterface
		}
	}
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.LocalizedInterface {
			var original contract.Profile
			if err := json.Unmarshal(policy.Cases[i].Expected, &original); err != nil {
				t.Fatal(err)
			}
			policy.Cases[i].Contract = contract.BrowserProfileInterface
			policy.Cases[i].Expected, _ = json.Marshal(contract.BrowserProfile{Profile: original, View: view})
		}
	}

	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, baseline, graph.Revision, policy.ID, "browser-install")
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err != nil {
		t.Fatal("current profile3 unavailable", err)
	}
	get := func(reference string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/apps/browser?interface_revision="+reference, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	read := func() {
		t.Helper()
		w := get(graph.Revision)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "poll-plan") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	read()
	style := func(reference, expected string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/apps/browser?interface_revision="+reference+"&format=css", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != expected || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
			t.Fatal("frozen stylesheet mismatch", w.Code, w.Body.String())
		}
	}
	style(graph.Revision, string(css))
	styledPackage, err := host.Install(ctx, browserFixturePackage(t, "1.23.0", true))
	if err != nil {
		t.Fatal(err)
	}
	styledGraph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-interface", PackageDigest: styledPackage.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-interface", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	stylePolicy, err := host.Store.Policy(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	stylePolicy.ID = "browser-style-owner"
	const change = "\n.app-account{padding:24px 0}"
	for i := range stylePolicy.Cases {
		if stylePolicy.Cases[i].Contract == contract.BrowserProfileInterface {
			var expected contract.BrowserProfile
			if err := json.Unmarshal(stylePolicy.Cases[i].Expected, &expected); err != nil {
				t.Fatal(err)
			}
			expected.View.CSS += change
			stylePolicy.Cases[i].Expected, _ = json.Marshal(expected)
		}
	}
	if err := host.Store.PutPolicy(ctx, stylePolicy); err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, graph.Revision, styledGraph.Revision, stylePolicy.ID, "browser-style-upgrade")
	style(styledGraph.Revision, string(css)+change)
	style(graph.Revision, string(css))
	activateInterface(t, ctx, host, styledGraph.Revision, graph.Revision, policy.ID, "browser-style-rollback")
	style(styledGraph.Revision, string(css)+change)
	style(graph.Revision, string(css))
	if w := get(old); w.Code != 503 {
		t.Fatal("old package invented browser source", w.Code)
	}
	activateInterface(t, ctx, host, graph.Revision, old, policyID, "browser-rollback")
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err != nil {
		t.Fatal("explicit profile2 rollback lost its legacy interpreter", err)
	}
	if _, err := a.appsInterfaceWithHost(ctx, host, old, "en"); err != nil {
		t.Fatal("original old reference rejected", err)
	}
	read()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	// First dependent read after recovery; no reselect or fixture repair.

	read()
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err != nil {
		t.Fatal("cold rollback lost original interpreter", err)
	}
	// A declared profile3 failure must never retry a healthy profile2, even
	// when an intentionally weak test owner admits both declared services.
	badPackage, err := host.Install(ctx, browserFixturePackage(t, "1.22.0", true))
	if err != nil {
		t.Fatal(err)
	}
	badGraph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-interface", PackageDigest: badPackage.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-interface", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	badPolicy, err := host.Store.Policy(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	badPolicy.ID = "browser-bad-owner"
	for i := range badPolicy.Cases {
		if badPolicy.Cases[i].Contract == contract.BrowserProfileInterface {
			var expected contract.BrowserProfile
			if err := json.Unmarshal(badPolicy.Cases[i].Expected, &expected); err != nil {
				t.Fatal(err)
			}
			expected.View.Worker = ""
			badPolicy.Cases[i].Expected, _ = json.Marshal(expected)
		}
	}
	oldPolicy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	badPolicy.AllowedContracts = append(badPolicy.AllowedContracts, contract.LocalizedInterface)
	for _, item := range oldPolicy.Cases {
		if item.Contract == contract.LocalizedInterface {
			badPolicy.Cases = append(badPolicy.Cases, item)
		}
	}
	if err := host.Store.PutPolicy(ctx, badPolicy); err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, old, badGraph.Revision, badPolicy.ID, "browser-bad-source")
	for _, reference := range []string{"", badGraph.Revision} {
		if _, err := a.appsInterfaceWithHost(ctx, host, reference, "en"); err == nil {
			t.Fatal("broken profile3 fell back to healthy profile2", reference)
		}
	}
	if w := get(badGraph.Revision); w.Code != 503 {
		t.Fatal("broken declared source available", w.Code)
	}
	read()
	// An old version label cannot replace an explicit profile contract. Missing
	// current and legacy profile declarations must fail, including explicit refs.
	missing, err := host.Install(ctx, browserFixturePackage(t, "0.1.0", false))
	if err != nil {
		t.Fatal(err)
	}
	missingGraph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-interface", PackageDigest: missing.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-interface", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}

	missingPolicy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	missingPolicy.ID = "browser-missing-owner"
	allowed := missingPolicy.AllowedContracts[:0]
	for _, name := range missingPolicy.AllowedContracts {
		if name != contract.LocalizedInterface {
			allowed = append(allowed, name)
		}
	}
	missingPolicy.AllowedContracts = allowed
	cases := missingPolicy.Cases[:0]
	for _, item := range missingPolicy.Cases {
		if item.Contract != contract.LocalizedInterface {
			cases = append(cases, item)
		}
	}
	missingPolicy.Cases = cases
	if err := host.Store.PutPolicy(ctx, missingPolicy); err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, badGraph.Revision, missingGraph.Revision, missingPolicy.ID, "browser-new-missing")
	for _, reference := range []string{"", missingGraph.Revision} {
		if _, err := a.appsInterfaceWithHost(ctx, host, reference, "en"); err == nil {
			t.Fatal("missing profile silently entered historical browser", reference)
		}
	}
	read()
	if _, err := a.appsInterfaceWithHost(ctx, host, old, "en"); err != nil {
		t.Fatal("new failure invalidated historical old reference", err)
	}
	if _, err := host.Store.Deactivate(ctx, "browser-disable", missingGraph.Revision); err != nil {
		t.Fatal(err)
	}
	read()
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err == nil {
		t.Fatal("deactivation restored native browser")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	// The first dependent read after reopening the deactivated store must fail;
	// historical source reads remain available without repairing the selection.
	if _, err := a.appsInterfaceWithHost(ctx, host, "", "en"); err == nil {
		t.Fatal("cold deactivation forgot browser boundary")
	}
	style(styledGraph.Revision, string(css)+change)
	style(graph.Revision, string(css))
	read()
}

// Current and independently changed client packages reuse the captured original
// data oracle; the owner keeps the reviewed client bytes separately from them.
func prepareBrowserInterface(t *testing.T, ctx context.Context, h rsi.Host, version string, accounts ...string) (string, string) {
	t.Helper()
	old, policyID := prepareInterface(t, ctx, h, "1.19.0", accounts...)
	pkg, err := h.Install(ctx, browserFixturePackage(t, version, true))
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Store.Graph(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	bindings := original.Graph.Bindings
	for i := range bindings {
		if bindings[i].InstanceID == "apps-interface" {
			bindings[i].PackageDigest = pkg.Digest
		}
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "browser-interface-" + version
	raw, err := os.ReadFile("../rsi/builtin/apps-client-owner-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var view contract.BrowserView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if version == "1.23.0" {
		view.CSS += "\n.app-account{padding:24px 0}"
	}
	for i, name := range policy.AllowedContracts {
		if name == contract.LocalizedInterface {
			policy.AllowedContracts[i] = contract.BrowserProfileInterface
		}
	}
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.LocalizedInterface {
			var expected contract.Profile
			if err := json.Unmarshal(policy.Cases[i].Expected, &expected); err != nil {
				t.Fatal(err)
			}
			policy.Cases[i].Contract = contract.BrowserProfileInterface
			policy.Cases[i].Expected, _ = json.Marshal(contract.BrowserProfile{Profile: expected, View: view})
		}
	}
	if err := h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, policy.ID
}
