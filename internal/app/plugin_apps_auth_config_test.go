package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsAuthConfigActualOriginalSourceChoiceVersionAndFences(t *testing.T) {
	ctx := context.Background()
	type record struct {
		ID, Toolkit, Status, Created string
		Managed, Router              bool
	}
	type page struct {
		Items  []record
		Next   string
		Status int
	}
	type fixture struct {
		Name, Toolkit, Override string
		Pages                   []page
		Expansion               bool
		Expected                struct {
			ID                      string
			Readable                bool
			ErrorStatus             int
			Failed                  bool
			Requests                []string
			NormalizedCreatedBytes  int
			NormalizedCreatedSHA256 string
		}
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-auth-config-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	if err := secrets.Put(ctx, "credential:auth-source", []byte("synthetic-native-auth-config-key")); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var mu sync.Mutex
	var active fixture
	noHosted := false
	index := 0
	var requests []string
	sessionPosts := 0
	selectedID := ""
	extraBad := false
	var seen, release chan struct{}
	mutateAfterSource := false
	var afterSource func()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		f := active
		knownNone := noHosted
		bad := extraBad
		if r.URL.Path == "/toolkits/example" {
			mu.Unlock()
			if knownNone {
				_, _ = w.Write([]byte(`{"slug":"example","name":"Example","composio_managed_auth_schemes":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"slug":"example","name":"Example"}`))
			}
			return
		}
		if r.URL.Path == "/tool_router/session" {
			sessionPosts++
			var body struct {
				AuthConfigs map[string]string `json:"auth_configs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			selectedID = body.AuthConfigs["example"]
			mu.Unlock()
			_, _ = w.Write([]byte(`{"session_id":"session_auth"}`))
			return
		}
		if r.URL.Path == "/tool_router/session/session_auth/link" {
			mu.Unlock()
			_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/auth","connected_account_id":"account_created"}`))
			return
		}
		if r.URL.Path == "/toolkits" {
			mu.Unlock()
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if r.URL.Path == "/tools" {
			mu.Unlock()
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/auth_configs") {
			mu.Unlock()
			http.NotFound(w, r)
			return
		}
		expectedKey := "synthetic-native-auth-config-key"
		if f.Expansion {
			expectedKey = "q"
		}
		if r.Header.Get("x-api-key") != expectedKey {
			t.Error("source credential scope changed")
		}
		requests = append(requests, r.URL.RequestURI())
		current := index
		index++
		entered, continueRead := seen, release
		if current == 0 {
			seen, release = nil, nil
		}
		after := afterSource
		afterSource = nil
		change := mutateAfterSource
		if change {
			mutateAfterSource = false
		}
		mu.Unlock()
		if current == 0 && entered != nil {
			close(entered)
			<-continueRead
		}
		if after != nil {
			after()
		}
		if change {
			state, e := a.store.LoadApps(ctx)
			if e != nil {
				t.Error(e)
			} else if e := a.store.SaveApps(ctx, state, state.Revision); e != nil {
				t.Error(e)
			}
		}
		explicit := r.URL.Path != "/auth_configs"
		p := page{}
		if explicit && f.Override == "" {
			id := strings.TrimPrefix(r.URL.Path, "/auth_configs/")
			p.Items = []record{{ID: id, Toolkit: "example", Status: "ENABLED", Router: true, Created: "2024"}}
		} else {
			if current >= len(f.Pages) {
				t.Error("repeated or excess source request")
				http.Error(w, "private diagnostics", http.StatusBadGateway)
				return
			}
			p = f.Pages[current]
		}
		if p.Status != 0 {
			http.Error(w, "private OAuth diagnostics", p.Status)
			return
		}
		type toolkit struct {
			Slug string `json:"slug"`
		}
		type credential struct {
			Secret string `json:"secret"`
		}
		type metadata struct {
			ID          string     `json:"id"`
			Toolkit     toolkit    `json:"toolkit"`
			Status      string     `json:"status"`
			Managed     bool       `json:"is_composio_managed"`
			Router      bool       `json:"is_enabled_for_tool_router"`
			Created     string     `json:"created_at"`
			Credentials credential `json:"credentials"`
		}
		items := make([]metadata, len(p.Items))
		for i, row := range p.Items {
			items[i] = metadata{ID: row.ID, Toolkit: toolkit{Slug: row.Toolkit}, Status: row.Status, Managed: row.Managed, Router: row.Router, Created: row.Created, Credentials: credential{Secret: "private-oauth-credential"}}
		}
		if f.Expansion {
			items[0].Created = strings.Repeat("q", 1<<20)
		}
		if bad && len(items) > 0 {
			items[0].Toolkit.Slug = "other"
		}
		if explicit {
			if len(items) != 1 {
				t.Error("invalid explicit fixture")
				http.Error(w, "invalid", http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(items[0])
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Items []metadata `json:"items"`
			Next  string     `json:"next_cursor"`
		}{items, p.Next})
	}))
	defer peer.Close()
	setup := func(f fixture, none bool) {
		mu.Lock()
		active = f
		noHosted = none
		index = 0
		requests = nil
		sessionPosts = 0
		selectedID = ""
		extraBad = false
		mu.Unlock()
		key := "synthetic-native-auth-config-key"
		if f.Expansion {
			key = "q"
		}
		if err := secrets.Put(ctx, "credential:auth-source", []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	stats := func() ([]string, int, string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...), sessionPosts, selectedID
	}
	var host rsi.Host
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
	}
	bind()
	save := func(saved string) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:auth-source"
		state.UserID = "owner_fixture"
		state.Connections = nil
		state.AuthConfigs = map[string]string{}
		if saved != "" {
			state.AuthConfigs["example"] = saved
		}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	for _, f := range fixtures {
		for _, none := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/known-none-%t", f.Name, none), func(t *testing.T) {
				setup(f, none)
				before := save("")
				if f.Expansion {
					client := composio.Client{Endpoint: peer.URL, Secrets: secrets, KeyRef: "credential:auth-source"}
					p, err := client.ReadAuthConfigPage(ctx, "example", "", 200)
					if err != nil || len(p.Items) != 2 {
						t.Fatal("original normalized source changed", err)
					}
					value := p.Items[0].Created
					sum := sha256.Sum256([]byte(value))
					if len(value) != f.Expected.NormalizedCreatedBytes || hex.EncodeToString(sum[:]) != f.Expected.NormalizedCreatedSHA256 {
						t.Fatal("full original expanded metadata was dropped")
					}
					encoded, _ := json.Marshal(p)
					if strings.Contains(string(encoded), "private-oauth-credential") {
						t.Fatal("source credentials escaped metadata decoder")
					}
					setup(f, none)
				}
				_, err := a.connectedApps.Connect(ctx, f.Toolkit, "work", f.Override)
				got, posts, id := stats()
				needConfig := !f.Expected.Failed && f.Expected.Readable && f.Expected.ID == "" && none
				failed := f.Expected.Failed || needConfig
				if (err != nil) != failed || !reflect.DeepEqual(got, f.Expected.Requests) {
					t.Fatal("native auth error/trace changed", err, got, f.Expected.Requests)
				}
				if needConfig && !errors.Is(err, connectedapps.ErrAuthConfigRequired) {
					t.Fatal("explicit empty hosted capability lost", err)
				}
				if failed {
					if posts != 0 {
						t.Fatal("failed auth selection started session", posts)
					}
					after, e := a.store.LoadApps(ctx)
					if e != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("failed auth selection wrote connection state", e)
					}
				} else if posts != 1 || id != f.Expected.ID {
					t.Fatal("selected original config did not reach actual session", posts, id, f.Expected.ID)
				}
			})
		}
	}
	good := func(id, created string) record {
		return record{ID: id, Toolkit: "example", Status: "ENABLED", Router: true, Created: created}
	}
	f := fixture{Name: "switch", Toolkit: "example", Pages: []page{{Items: []record{good("ac_old", "2025")}, Next: "opaque +/=&"}, {Items: []record{good("ac_new", "2026")}}}}
	// Preserve explicit and saved sources; candidate never receives invented IDs.
	setup(f, false)
	save("ac_saved")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", "ac_explicit"); err != nil {
		t.Fatal(err)
	}
	got, posts, id := stats()
	if posts != 1 || id != "ac_explicit" || !reflect.DeepEqual(got, []string{"/auth_configs/ac_explicit"}) {
		t.Fatal("explicit owner config was rewritten", got, id)
	}
	setup(f, false)
	save("ac_saved")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	got, posts, id = stats()
	if posts != 1 || id != "ac_saved" || !reflect.DeepEqual(got, []string{"/auth_configs/ac_saved"}) {
		t.Fatal("saved source choice changed", got, id)
	}
	// Change versions during the first actual source page. Both subsequent
	// selection and the second request retain the originally frozen algorithm.
	setup(f, false)
	save("")
	entered, continueRead := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(continueRead) })
	defer releaseSource()
	mu.Lock()
	seen, release = entered, continueRead
	mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := a.connectedApps.Connect(ctx, "example", "work", ""); done <- err }()
	<-entered
	second, _ := activateAccountIntent(t, ctx, host, first, "1.2.0")
	releaseSource()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, posts, id = stats()
	if posts != 1 || id != "ac_new" || len(got) != 2 || !strings.Contains(got[0], "limit=200") || !strings.Contains(got[1], "limit=200") {
		t.Fatal("inflight auth flow reselected graph", got, id)
	}
	setup(f, false)
	save("")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	got, posts, id = stats()
	if posts != 1 || id != "ac_old" || len(got) != 2 || !strings.Contains(got[0], "limit=100") || !strings.Contains(got[1], "limit=100") {
		t.Fatal("independent selector/pagination did not change actual source", got, id)
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
	reopen()
	setup(f, false)
	save("")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	_, posts, id = stats()
	if posts != 1 || id != "ac_old" {
		t.Fatal("cold auth policy lost selected version", id)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "auth-config-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original auth configuration workflow"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "auth-config-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "auth-config-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(f, false)
	save("")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); err != nil {
		t.Fatal(err)
	}
	_, posts, id = stats()
	if posts != 1 || id != "ac_new" {
		t.Fatal("cold auth rollback failed", id)
	}
	// Original private state changes after source read but before pure result
	// acceptance: no configuration or hosted session may be published.
	setup(f, false)
	save("")
	mu.Lock()
	mutateAfterSource = true
	mu.Unlock()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrConflict) {
		t.Fatal("late source/state change escaped private fence", err)
	}
	_, posts, _ = stats()
	if posts != 0 {
		t.Fatal("late state change reached session")
	}
	// Configuration UI validation is a second real consumer, not a native bypass.
	setup(f, false)
	state := save("")
	if err := a.connectedApps.ConfigureProject(ctx, state.KeyRef, state.Revision, map[string]string{"example": "ac_saved"}); err != nil {
		t.Fatal("actual config validation failed", err)
	}
	got, posts, _ = stats()
	if posts != 0 || !reflect.DeepEqual(got, []string{"/auth_configs/ac_saved"}) {
		t.Fatal("config validation used wrong source", got)
	}
	third, _ := activateAccountIntent(t, ctx, host, first, "2.0.0")
	for _, count := range []int{7, 8, 9} {
		rows := make([]record, count)
		for i := range rows {
			rows[i] = good(fmt.Sprintf("ac_%02d", i), "2026")
		}
		bad := fixture{Toolkit: "example", Pages: []page{{Items: rows}}}
		setup(bad, false)
		if count == 8 {
			mu.Lock()
			extraBad = true
			mu.Unlock()
		}
		before := save("")
		if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
			t.Fatal("invalid auth reference/command escaped", count, err)
		}
		got, posts, _ = stats()
		if posts != 0 || len(got) != 1 {
			t.Fatal("invalid plan replayed source or started session", count, got, posts)
		}
		after, e := a.store.LoadApps(ctx)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("invalid plan changed owner state", count, e)
		}
	}
	setup(f, false)
	save("ac_saved")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", "ac_explicit"); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("malicious policy rewrote explicit owner source", err)
	}
	got, posts, _ = stats()
	if len(got) != 0 || posts != 0 {
		t.Fatal("rewritten explicit source reached broker", got, posts)
	}
	setup(f, false)
	save("")
	mu.Lock()
	afterSource = func() {
		if err := host.Store.Quarantine(ctx, third); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("source-time quarantine accepted metadata", err)
	}
	got, posts, _ = stats()
	if len(got) != 1 || posts != 0 {
		t.Fatal("late quarantine retried source or started session", got, posts)
	}
	setup(f, false)
	save("")
	if err := host.Store.Quarantine(ctx, third); err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("quarantine restored native auth flow", err)
	}
	got, posts, _ = stats()
	if len(got) != 0 || posts != 0 {
		t.Fatal("quarantine contacted auth source")
	}
	if _, err := host.Store.Deactivate(ctx, "auth-config-disable", third); err != nil {
		t.Fatal(err)
	}
	reopen()
	setup(f, false)
	save("")
	if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("cold disable restored native policy", err)
	}
	got, posts, _ = stats()
	if len(got) != 0 || posts != 0 {
		t.Fatal("cold disable contacted source")
	}
}
