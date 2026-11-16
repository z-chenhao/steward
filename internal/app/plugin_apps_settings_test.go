package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

type settingsHTTPGateway struct {
	web.AppConnections
	calls   int
	configs map[string]string
}

func (g *settingsHTTPGateway) ConfigureProject(c context.Context, key string, revision int64, configs map[string]string) error {
	g.calls++
	g.configs = configs
	return g.AppConnections.ConfigureProject(c, key, revision, configs)
}

type settingsHTTPCase struct {
	Name, Method, Action, Revision, Serialized string
	Apps, IDs                                  []string
	Count                                      int
	Saved                                      map[string]string
	Expected                                   struct {
		Code, Calls             int
		Rows                    []connectedapps.SettingsRow
		KeyRef, Revision        string
		Unsaved, Saved, Invalid bool
		Configs, Dispatched     map[string]string
		RevisionDelta           int64
		Requests                []string
	}
}

func TestAppsSettingsActualOriginalHTTPRowsDraftAndSave(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	token := "synthetic-settings-token-at-least-24-bytes"
	_ = secrets.Put(ctx, "credential:test", []byte(token))
	_ = secrets.Put(ctx, "env:COMPOSIO_API_KEY", []byte("synthetic-settings-key"))
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	store := a.store
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	request := func(method, path, language, body string) *http.Request {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "steward-language", Value: language})
		return r
	}
	newServer := func() *web.Server {
		return &web.Server{Config: web.Config{TokenRef: "credential:test"}, Core: a, Secrets: secrets}
	}

	requests := []string{}
	var afterCatalogue func()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.URL.Path == "/toolkits" {
			if afterCatalogue != nil {
				f := afterCatalogue
				afterCatalogue = nil
				f()
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/auth_configs/") {
			id := strings.TrimPrefix(r.URL.Path, "/auth_configs/")
			app := strings.TrimPrefix(id, "ac_")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "toolkit": map[string]string{"slug": app}, "status": "ENABLED", "is_enabled_for_tool_router": true})
			return
		}
		t.Error("unexpected source", r.URL)
		http.NotFound(w, r)
	}))
	defer peer.Close()
	service := a.connectedApps
	service.Client = composio.Client{Endpoint: peer.URL}
	service.Changed = nil
	service.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) { return a.appsSettingsWithHost(c, host) }
	service.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
		return a.appsAccountPoliciesWithHost(c, host)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-settings-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []settingsHTTPCase
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	appsPattern := regexp.MustCompile(`name="app_slug" value="([^"]*)"`)
	idPattern := regexp.MustCompile(`name="auth_config" value="([^"]*)"`)
	keyPattern := regexp.MustCompile(`id="composio-key-ref" name="key_ref" value="([^"]*)"`)
	revPattern := regexp.MustCompile(`name="revision" value="([^"]*)"`)
	for i := range fixtures {
		f := &fixtures[i]
		t.Run(f.Name, func(t *testing.T) {
			original := f.Expected
			f.Expected = struct {
				Code, Calls             int
				Rows                    []connectedapps.SettingsRow
				KeyRef, Revision        string
				Unsaved, Saved, Invalid bool
				Configs, Dispatched     map[string]string
				RevisionDelta           int64
				Requests                []string
			}{}

			state, err := store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state.KeyRef = "env:COMPOSIO_API_KEY"
			state.UserID = "owner"
			state.AuthConfigs = f.Saved
			state.Connections = nil
			if f.Method == "GET" && f.Count > 0 {
				state.AuthConfigs = map[string]string{}
				for n := 0; n < f.Count; n++ {
					app := fmt.Sprintf("app_%03d", n)
					state.AuthConfigs[app] = "ac_" + app
				}
			}
			if err := store.SaveApps(ctx, state, state.Revision); err != nil {
				t.Fatal(err)
			}
			before, err := store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requests = []string{}
			gateway := &settingsHTTPGateway{AppConnections: service}
			server := newServer()
			server.Apps = gateway
			values := url.Values{"editor": {"fields"}, "revision": {strconv.FormatInt(before.Revision, 10)}, "key_ref": {"env:COMPOSIO_API_KEY"}, "action": {f.Action}}
			if f.Revision == "bad" {
				values.Set("revision", "not-a-number")
			}
			if f.Revision == "stale" {
				values.Set("revision", strconv.FormatInt(before.Revision-1, 10))
			}
			apps, ids := f.Apps, f.IDs
			if f.Count > 0 && f.Method != "GET" {
				for n := 0; n < f.Count; n++ {
					app := fmt.Sprintf("app_%03d", n)
					apps = append(apps, app)
					ids = append(ids, "ac_"+app)
				}
			}
			values["app_slug"], values["auth_config"] = apps, ids
			if f.Serialized != "" {
				values.Del("editor")
				text := f.Serialized
				if text == "EMPTY" {
					text = ""
				}
				if text == "LARGE" {
					text = strings.Repeat(" ", 16385) + "{}"
				}
				values.Set("auth_configs", text)
			}
			w := httptest.NewRecorder()
			if f.Method == "GET" {
				server.ServeHTTP(w, request("GET", "/settings/apps/service", "en", ""))
			} else {
				server.ServeHTTP(w, request("POST", "/apps/service/configure", "en", values.Encode()))
			}
			body := w.Body.String()
			rowApps, rowIDs := appsPattern.FindAllStringSubmatch(body, -1), idPattern.FindAllStringSubmatch(body, -1)
			if len(rowApps) != len(rowIDs) {
				t.Fatal("mismatched native rendered rows")
			}
			f.Expected.Rows = []connectedapps.SettingsRow{}
			for i, row := range rowApps {
				f.Expected.Rows = append(f.Expected.Rows, connectedapps.SettingsRow{App: html.UnescapeString(row[1]), Config: html.UnescapeString(rowIDs[i][1])})
			}
			key, revision := keyPattern.FindStringSubmatch(body), revPattern.FindStringSubmatch(body)
			if len(key) != 2 || len(revision) != 2 {
				t.Fatal("missing native settings fields", w.Code)
			}
			f.Expected.KeyRef = html.UnescapeString(key[1])
			f.Expected.Revision = revision[1]
			if f.Expected.Revision == strconv.FormatInt(before.Revision, 10) {
				f.Expected.Revision = "before"
			} else if f.Expected.Revision == strconv.FormatInt(before.Revision+1, 10) {
				f.Expected.Revision = "after"
			} else if f.Expected.Revision == strconv.FormatInt(before.Revision-1, 10) {
				f.Expected.Revision = "stale"
			}
			f.Expected.Code = w.Code
			f.Expected.Calls = gateway.calls
			f.Expected.Unsaved = strings.Contains(body, "Unsaved changes.")
			f.Expected.Saved = strings.Contains(body, "App connection service saved.")
			f.Expected.Invalid = w.Code == 422
			after, err := store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.Expected.Configs = after.AuthConfigs
			f.Expected.Dispatched = gateway.configs
			f.Expected.RevisionDelta = after.Revision - before.Revision
			sort.Strings(requests)
			f.Expected.Requests = append([]string{}, requests...)
			if !reflect.DeepEqual(f.Expected, original) {
				t.Fatalf("original settings HTTP behavior changed\ngot=%#v\nwant=%#v", f.Expected, original)
			}
		})
	}
	setup := func(configs map[string]string) connectedapps.State {
		t.Helper()
		state, err := store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "env:COMPOSIO_API_KEY"
		state.UserID = "owner"
		state.AuthConfigs = configs
		state.Connections = nil
		if err := store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		requests = []string{}
		return state
	}
	bind := func() {
		store = a.store
		service = a.connectedApps
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		service.Client = composio.Client{Endpoint: peer.URL}
		service.Changed = nil
		service.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) { return a.appsSettingsWithHost(c, host) }
		service.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
	}
	reopen := func() {
		t.Helper()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
		if err != nil {
			t.Fatal(err)
		}
		a.eventsRequired.Store(false)
		bind()
	}
	rendered := func(body string) []connectedapps.SettingsRow {
		aa, ii := appsPattern.FindAllStringSubmatch(body, -1), idPattern.FindAllStringSubmatch(body, -1)
		if len(aa) != len(ii) {
			t.Fatal("lost original row pairs")
		}
		out := []connectedapps.SettingsRow{}
		for i, v := range aa {
			out = append(out, connectedapps.SettingsRow{App: html.UnescapeString(v[1]), Config: html.UnescapeString(ii[i][1])})
		}
		return out
	}
	get := func() *httptest.ResponseRecorder {
		server := newServer()
		server.Apps = service
		w := httptest.NewRecorder()
		server.ServeHTTP(w, request("GET", "/settings/apps/service", "en", ""))
		return w
	}
	send := func(action string, apps, ids []string) (*httptest.ResponseRecorder, int) {
		state, err := store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v := url.Values{"editor": {"fields"}, "revision": {strconv.FormatInt(state.Revision, 10)}, "key_ref": {"env:COMPOSIO_API_KEY"}, "action": {action}, "app_slug": apps, "auth_config": ids}
		gateway := &settingsHTTPGateway{AppConnections: service}
		server := newServer()
		server.Apps = gateway
		w := httptest.NewRecorder()
		server.ServeHTTP(w, request("POST", "/apps/service/configure", "en", v.Encode()))
		return w, gateway.calls
	}

	browserCheck := func(version string, order []string, blankFirst bool) {
		t.Helper()
		if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
			return
		}
		setup(map[string]string{"a": "ac_a", "b": "ac_b"})
		before, err := store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		server := newServer()
		server.Apps = service
		served := httptest.NewServer(server)
		defer served.Close()
		evidence := os.Getenv("STEWARD_APP_SETTINGS_BROWSER_EVIDENCE")
		if evidence == "" {
			evidence = t.TempDir()
		}
		input, _ := json.Marshal(struct {
			URL, Token, Evidence string
			Order                []string
			BlankFirst           bool
		}{served.URL, token, filepath.Join(evidence, version), order, blankFirst})
		browserCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(browserCtx, "node", "../../tests/system/apps_settings_plugins_browser.mjs")
		cmd.Stdin = strings.NewReader(string(input))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("settings browser: %v\n%s", err, out)
		}
		after, err := store.LoadApps(ctx)
		if err != nil || after.Revision != before.Revision {
			t.Fatal("browser draft became save", after, err)
		}
	}
	browserCheck("managed-1.0.0", []string{"a", "b"}, false)
	pair := map[string]string{"a": "ac_a", "b": "ac_b"}
	setup(pair)
	seen, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	service.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) {
		selected, err := a.appsSettingsWithHost(c, host)
		close(seen)
		<-proceed
		return selected, err
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- get() }()
	<-seen
	second, _ := activateAccountIntent(t, ctx, host, first, "1.7.0")
	unblock()
	old := <-done
	if old.Code != 200 || !reflect.DeepEqual(rendered(old.Body.String()), []connectedapps.SettingsRow{{App: "a", Config: "ac_a"}, {App: "b", Config: "ac_b"}}) {
		t.Fatal("captured settings version reselected", old.Code)
	}
	bind()
	browserCheck("managed-1.7.0", []string{"b", "a"}, true)
	fresh := get()
	if fresh.Code != 200 || rendered(fresh.Body.String())[0].App != "b" {
		t.Fatal("new descending settings recipe not used", fresh.Code)
	}
	before := setup(pair)
	draft, calls := send("add-override", []string{"a", "b"}, []string{"ac_a", "ac_b"})
	if draft.Code != 200 || calls != 0 || !reflect.DeepEqual(rendered(draft.Body.String()), []connectedapps.SettingsRow{{}, {App: "a", Config: "ac_a"}, {App: "b", Config: "ac_b"}}) {
		t.Fatal("independent blank-row placement or owner draft intent lost", draft.Code, calls)
	}
	state, err := store.LoadApps(ctx)
	if err != nil || state.Revision != before.Revision {
		t.Fatal("draft became persistence", state, err)
	}
	reopen()
	fresh = get()
	if fresh.Code != 200 || rendered(fresh.Body.String())[0].App != "b" {
		t.Fatal("cold settings version lost", fresh.Code)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "settings-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original settings projection"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "settings-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "settings-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(pair)
	fresh = get()
	if fresh.Code != 200 || rendered(fresh.Body.String())[0].App != "a" {
		t.Fatal("cold settings rollback lost", fresh.Code)
	}
	// Original already-accepted maps span multiple output windows and the normal
	// pure-input ceiling. Hash expectations came from the exact old Web overlay.
	largeRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-settings-large-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var large []struct {
		Name              string
		Count, LargeBytes int
		AppsSHA256        string
	}
	if err := json.Unmarshal(largeRaw, &large); err != nil {
		t.Fatal(err)
	}
	largeMaps := map[string]map[string]string{}
	for _, f := range large {
		configs := map[string]string{}
		for n := 0; n < f.Count-1; n++ {
			configs[fmt.Sprintf("app_%05d", n)] = "ac_original"
		}
		configs[strings.Repeat("z", f.LargeBytes)] = "ac_large"
		largeMaps[f.Name] = configs
		setup(configs)
		response := get()
		if response.Code != 200 {
			t.Fatal("original large settings unavailable", f.Name, response.Code)
		}
		rows := rendered(response.Body.String())
		hash := sha256.New()
		for _, row := range rows {
			_, _ = hash.Write(binary.LittleEndian.AppendUint64(nil, uint64(len(row.App))))
			_, _ = hash.Write([]byte(row.App))
		}
		if len(rows) != f.Count || hex.EncodeToString(hash.Sum(nil)) != f.AppsSHA256 {
			t.Fatal("original native full settings rows changed", f.Name, len(rows))
		}
	}
	input, _ := json.Marshal(contract.SettingsRequest{Mode: "saved", Count: 1, Remove: -1})
	if _, err := host.ReadService(ctx, first, contract.Settings, input, []rsi.InputData{{ID: contract.SettingsInputID, Data: contract.EncodeSettingsApps([]string{strings.Repeat("z", 9<<20)})}}); err == nil {
		t.Fatal("ordinary pure input limit widened")
	}
	// Original source revision is checked before launching a candidate, even when
	// the carrier is absent. Submitted draft revisions are never read authority.
	setup(pair)
	service.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) {
		selected, err := a.appsSettingsWithHost(c, host)
		v, e := store.LoadApps(c)
		if e != nil {
			t.Fatal(e)
		}
		if e := store.SaveApps(c, v, v.Revision); e != nil {
			t.Fatal(e)
		}
		return selected, err
	}
	if err := os.Rename(carrier, carrier+".held"); err != nil {
		t.Fatal(err)
	}
	stale := get()
	if err := os.Rename(carrier+".held", carrier); err != nil {
		t.Fatal(err)
	}
	if stale.Code < 400 || strings.Contains(stale.Body.String(), `value="ac_a"`) {
		t.Fatal("stale settings launched/released private rows", stale.Code)
	}
	bind()
	baseline := first
	for i, version := range []string{"7.0.0", "7.1.0", "7.2.0", "7.3.0", "7.4.0"} {
		graph, _ := activateAccountIntent(t, ctx, host, baseline, version)
		baseline = graph
		configs := pair
		if i == 4 {
			configs = largeMaps["windows16387"]
		}
		before = setup(configs)
		var response *httptest.ResponseRecorder
		calls := 0
		switch i {
		case 2:
			response, calls = send("add-override", []string{"a", "b"}, []string{"ac_a", "ac_b"})
		case 3:
			response, calls = send("remove-override-0", []string{"a", "b"}, []string{"ac_a", "ac_b"})
		default:
			response = get()
		}
		state, err = store.LoadApps(ctx)
		if response.Code < 400 || calls != 0 || err != nil || state.Revision != before.Revision || strings.Contains(response.Body.String(), `name="app_slug" value="a"`) {
			t.Fatal("forged settings projection released rows or wrote config", version, response.Code, calls, state.Revision, before.Revision, err)
		}
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "settings-restore", Baseline: baseline, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original to test accepted save scope"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "settings-restore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "settings-restore", Expected: baseline, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	// A real source-time graph switch cannot reselect authentication or the
	// already planned response. An attempted extra selector read is a hard error.
	setup(map[string]string{})
	service.ReadAccountPolicies = func(context.Context) (connectedapps.AccountPolicies, error) {
		return connectedapps.AccountPolicies{}, errors.New("unexpected graph reselection")
	}
	afterCatalogue = func() {
		if err := host.Propose(ctx, rsi.Candidate{ID: "settings-source-beta", Baseline: first, Target: second, PolicyID: "accounts-owner-1.7.0", Goal: "Change active settings during exact original save"}); err != nil {
			t.Error(err)
			return
		}
		d, err := host.Verify(ctx, "settings-source-beta")
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := host.Activate(ctx, rsi.Activation{OperationID: "settings-source-beta", Expected: first, Target: second, DecisionID: d}); err != nil {
			t.Error(err)
		}
	}
	saved, calls := send("", []string{"b", "a"}, []string{"ac_b", "ac_a"})
	if saved.Code != 200 || calls != 1 || !strings.Contains(saved.Body.String(), "App connection service saved.") || rendered(saved.Body.String())[0].App != "a" {
		t.Fatal("source-time graph change reselected auth/response or lost accepted save", saved.Code, calls)
	}
	bind()
	setup(map[string]string{})
	spy := &accountCloseStore{Store: store, afterSave: func(n int) {
		if n == 1 {
			if err := host.Store.Quarantine(ctx, second); err != nil {
				t.Error(err)
			}
		}
	}}
	service.Store = spy
	saved, calls = send("", []string{"a", "b"}, []string{"ac_a", "ac_b"})
	if saved.Code != 200 || calls != 1 || rendered(saved.Body.String())[0].App != "b" {
		t.Fatal("post-save quarantine hid already accepted config", saved.Code, calls)
	}
	state, err = store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(state.AuthConfigs, pair) {
		t.Fatal("accepted original config changed", state, err)
	}
	service.Store = store
	response := get()
	if response.Code < 400 {
		t.Fatal("quarantined settings restored native", response.Code)
	}
	if _, err := host.Store.Deactivate(ctx, "settings-disable", second); err != nil {
		t.Fatal(err)
	}
	reopen()
	response = get()
	if response.Code < 400 {
		t.Fatal("cold disabled settings restored native", response.Code)
	}

}
