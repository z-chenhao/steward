package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const alternateLogo = `<svg viewBox="0 0 2 2"><path fill="#13579b" d="M0 0h2v2H0z"/></svg>`

func appsLogoPackage(t *testing.T, version string) string {
	t.Helper()
	if version == "1.0.0" {
		return defaultFeaturePackage(t, "apps-logos", nil)
	}
	path, err := filepath.Abs("../plugin/appslogos/logo.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, "const seconds = 86400", "const seconds = 300", 1)
		code = strings.Replace(code, "int64(24*time.Hour)", "int64(300*time.Second)", 1)
		marker := "return contract.Plan{Kind: \"bundled\", Body: body, CacheSeconds: seconds}"
		if !strings.Contains(code, marker) {
			t.Fatal("artwork variation marker missing")
		}
		code = strings.Replace(code, marker, `if input.Slug=="gmail" {body=[]byte(`+"`"+alternateLogo+"`"+`)};`+marker, 1)
	}
	if version == "2.0.0" {
		marker := "return rsiplugin.JSONResult(ctx, choose(input))"
		if !strings.Contains(code, marker) {
			t.Fatal("owner-read variation marker missing")
		}
		code = strings.Replace(code, marker, `if call.OperationID=="owner-read" {return rsiplugin.JSONResult(ctx,contract.Plan{Kind:"bundled",Body:[]byte("<html/>"),CacheSeconds:86400})};`+marker, 1)
	}
	dir := t.TempDir()
	override := filepath.Join(t.TempDir(), "logo.go")
	if err := os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-logos").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/apps-logos/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
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

func prepareAppsLogo(t *testing.T, ctx context.Context, host rsi.Host, version string) (string, string) {
	t.Helper()
	pkg, err := host.Install(ctx, appsLogoPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-logos", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-logos", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	seconds := 86400
	if version == "1.1.0" {
		seconds = 300
	}
	policy := rsi.Policy{ID: "logos-" + version, AllowedContracts: []string{contract.Logo}}
	for _, f := range []struct {
		registered, cached bool
		kind               string
	}{{false, false, "unavailable"}, {true, false, "provider"}, {false, true, "cached"}} {
		input, _ := json.Marshal(contract.Request{Slug: "original_unbundled", Registered: f.registered, Cached: f.cached})
		output, _ := json.Marshal(contract.Plan{Kind: f.kind, CacheSeconds: seconds})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-logos", Contract: contract.Logo, Input: input, Expected: output})
	}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, policy.ID
}
func activateAppsLogo(t *testing.T, ctx context.Context, host rsi.Host, baseline, target, policy, operation string) {
	t.Helper()
	if err := host.Propose(ctx, rsi.Candidate{ID: operation, Baseline: baseline, Target: target, PolicyID: policy, Goal: "Retain original logo HTTP while independently versioning actual artwork and cache"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: operation, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
}

func TestAppsLogoActualOwnerHTTPOriginal65FrozenReferencesColdAndDisable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-logo-owner-token-at-least24"
	if err := secrets.Put(ctx, "credential:logo-owner", []byte(token)); err != nil {
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
	buildTestBinary(t, "../../cmd/steward", carrier)
	var host rsi.Host
	var server *web.Server
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) { return a.appsSettingsWithHost(c, host) }
		server = &web.Server{Config: web.Config{TokenRef: "credential:logo-owner"}, Core: a, Apps: a.connectedApps, Secrets: secrets, Logos: func(c context.Context, reference string) (web.AppLogoPolicy, error) {
			return a.appsLogosWithHost(c, host, reference)
		}}
	}
	bind()
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	oldGraph, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	original := get("/apps/logo/gmail")
	if original.Code != 200 {
		t.Fatal(original.Code, original.Body.String())
	}
	oldNative, err := a.appsLogosWithHost(ctx, host, "")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareAppsLogo(t, ctx, host, "1.0.0")
	activateAppsLogo(t, ctx, host, oldGraph, first, firstPolicy, "logos-first")
	read := server.Logos
	server.Logos = func(context.Context, string) (web.AppLogoPolicy, error) { return oldNative, nil }
	if w := get("/apps/logo/gmail"); w.Code != 200 || w.Body.String() != original.Body.String() {
		t.Fatal("captured pre-opt-in request changed")
	}
	server.Logos = read
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-logos-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Slug string
		Expected   []struct {
			Code                                      int
			Kind, ETag, CacheControl, CSP, BodySHA256 string
			Bytes                                     int
		}
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range fixtures {
		if !strings.HasPrefix(f.Name, "bundled-") {
			continue
		}
		count++
		w := get("/apps/logo/" + f.Slug)
		hash := sha256.Sum256(w.Body.Bytes())
		want := f.Expected[0]
		if w.Code != want.Code || w.Header().Get("ETag") != want.ETag || w.Header().Get("Content-Type") != want.Kind || w.Header().Get("Cache-Control") != want.CacheControl || w.Header().Get("Content-Security-Policy") != want.CSP || hex.EncodeToString(hash[:]) != want.BodySHA256 || w.Body.Len() != want.Bytes {
			t.Fatal("original actual App resource changed", f.Slug, w.Code)
		}
	}
	if count != 65 {
		t.Fatal("bundled count", count)
	}
	if w := get("/apps/logo/unregistered"); w.Code != 404 {
		t.Fatal("candidate fetched unregistered source")
	}
	page := get("/settings/apps?view=all")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "/apps/logo/gmail?revision="+url.QueryEscape(first)) {
		t.Fatal("actual page did not freeze resource graph", page.Code)
	}
	after, err := a.store.LoadApps(ctx)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("GET wrote account state", err)
	}
	var calls int
	if err := a.store.Plugins().DB.QueryRowContext(ctx, "SELECT count(*) FROM rsi_call").Scan(&calls); err != nil || calls != 0 {
		t.Fatal("logo read created production invocation", calls, err)
	}

	browserCheck := func(version, revision, etag string) {
		t.Helper()
		if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
			return
		}
		peer := httptest.NewServer(server)
		defer peer.Close()
		folder := os.Getenv("STEWARD_APP_LOGO_BROWSER_EVIDENCE")
		if folder == "" {
			folder = filepath.Join(t.TempDir(), "logos")
		}
		input, _ := json.Marshal(struct{ URL, Token, Evidence, Revision, OriginalURL, OriginalETag, CurrentETag string }{peer.URL, token, filepath.Join(folder, version), revision, "/apps/logo/gmail?revision=" + url.QueryEscape(first), original.Header().Get("ETag"), etag})
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		command := exec.CommandContext(c, "node", "../../tests/system/apps_logos_plugins_browser.mjs")
		command.Stdin = bytes.NewReader(input)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	browserCheck("managed-1.0.0", first, original.Header().Get("ETag"))
	second, secondPolicy := prepareAppsLogo(t, ctx, host, "1.1.0")
	activateAppsLogo(t, ctx, host, first, second, secondPolicy, "logos-second")
	changed := get("/apps/logo/gmail")
	if changed.Code != 200 || !strings.Contains(changed.Body.String(), "#13579b") || changed.Header().Get("Cache-Control") != "private, max-age=300" || changed.Header().Get("ETag") == original.Header().Get("ETag") {
		t.Fatal("independent artwork/cache version not used", changed.Code, changed.Body.String())
	}
	browserCheck("managed-1.1.0", second, changed.Header().Get("ETag"))
	firstURL := "/apps/logo/gmail?revision=" + url.QueryEscape(first)
	if w := get(firstURL); w.Code != 200 || w.Body.String() != original.Body.String() {
		t.Fatal("old published graph resource changed", w.Code)
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
	if w := get(firstURL); w.Code != 200 || w.Body.String() != original.Body.String() {
		t.Fatal("cold retained resource lost", w.Code)
	}
	if w := get("/apps/logo/gmail"); w.Code != 200 || !strings.Contains(w.Body.String(), "#13579b") {
		t.Fatal("cold active artwork lost", w.Code)
	}
	activateAppsLogo(t, ctx, host, second, first, firstPolicy, "logos-rollback")
	if w := get("/apps/logo/gmail"); w.Code != 200 || w.Body.String() != original.Body.String() {
		t.Fatal("rollback used stale new artwork")
	}
	browserCheck("rollback-1.0.0", first, original.Header().Get("ETag"))
	bad, badPolicy := prepareAppsLogo(t, ctx, host, "2.0.0")
	activateAppsLogo(t, ctx, host, first, bad, badPolicy, "logos-bad")
	if w := get("/apps/logo/gmail"); w.Code != 404 {
		t.Fatal("invalid candidate restored native image")
	}
	if err := host.Store.Quarantine(ctx, first); err != nil {
		t.Fatal(err)
	}
	if w := get(firstURL); w.Code != 404 {
		t.Fatal("quarantined old cached resource released")
	}
	activateAppsLogo(t, ctx, host, bad, oldGraph, "accounts-owner-1.0.0", "logos-old-graph")
	if w := get("/apps/logo/gmail"); w.Code != 404 {
		t.Fatal("old graph without logos restored native")
	}
	if w := get("/settings/apps/service"); w.Code != 200 {
		t.Fatal("independent seven-service account graph lost settings", w.Code)
	}
	if _, err := host.Store.Deactivate(ctx, "logos-disable", oldGraph); err != nil {
		t.Fatal(err)
	}
	if w := get("/apps/logo/gmail"); w.Code != 404 {
		t.Fatal("deactivation restored native")
	}
	if w := get("/settings/apps?view=all"); w.Code != 200 || strings.Contains(w.Body.String(), `src="/apps/logo/`) {
		t.Fatal("missing logo service damaged independent directory or retained native image fallback", w.Code)
	}
	if w := get("/settings/apps/service"); w.Code != 503 {
		t.Fatal("deactivated independently opted-in settings restored native", w.Code)
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
	if w := get("/apps/logo/gmail"); w.Code != 404 {
		t.Fatal("cold disabled fell back to native")
	}
}
