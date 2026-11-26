package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsAccountIntentActualNativeEffectsVersionColdAndPrivateFences(t *testing.T) {
	ctx := context.Background()
	type trace struct{ Method, URI, Alias string }
	type outcome struct {
		Error              string
		Link               composio.Link
		NewRows            []connectedapps.Connection
		Requests           []trace
		Saves, Changed     int
		OriginalRowsSHA256 string
	}
	var fixtures []struct {
		Name, Alias         string
		Rows                []connectedapps.Connection
		Pending             []string
		NoAuth, FailSession bool
		Generated           *struct{ Count, AliasRepeats int }
		Expected            outcome
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-connect-intent-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-account-intent-owner-token-24-characters"
	if err := secrets.Put(ctx, "credential:account-owner", []byte(token)); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, "credential:native-connect", []byte("synthetic-native-key")); err != nil {
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
	nonce := regexp.MustCompile(`^personal_[a-f0-9]{8}$`)
	normalizeAlias := func(alias string) string {
		if nonce.MatchString(alias) {
			return "personal_<nonce>"
		}
		return alias
	}
	var peerMu sync.Mutex
	noAuth, failSession := false, false
	var requests []trace
	var toolkitSeen, toolkitRelease chan struct{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "synthetic-native-key" {
			t.Error("original credential scope changed")
		}
		if r.URL.Path == "/tools" {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		} // Separate existing namespace refresh consumer.
		item := trace{Method: r.Method, URI: r.URL.RequestURI()}
		linkAlias := ""
		if strings.HasSuffix(r.URL.Path, "/link") {
			var body struct {
				Alias string `json:"alias"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			linkAlias = body.Alias
			item.Alias = normalizeAlias(body.Alias)
		}
		peerMu.Lock()
		requests = append(requests, item)
		public, fail := noAuth, failSession
		seen, release := toolkitSeen, toolkitRelease
		if r.URL.Path == "/toolkits/example" {
			toolkitSeen, toolkitRelease = nil, nil
		}
		peerMu.Unlock()
		switch r.URL.Path {
		case "/toolkits/example":
			if seen != nil {
				close(seen)
				<-release
			}
			_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: "example", Name: "Original example", NoAuth: public})
		case "/auth_configs":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/tool_router/session":
			if fail {
				http.Error(w, "private source body", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"session_id":"session_fixture"}`))
		case "/tool_router/session/session_fixture/link":
			link := composio.Link{URL: "https://connect.composio.dev/link/fixture", AccountID: "account_created"}
			if linkAlias == "older" || linkAlias == "newer" {
				link.URL = "https://connect.composio.dev/link/" + linkAlias
				link.AccountID = "account_" + linkAlias
			}
			_ = json.NewEncoder(w).Encode(link)
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	mode := func(public, fail bool) {
		peerMu.Lock()
		noAuth, failSession = public, fail
		requests = nil
		peerMu.Unlock()
	}
	clear := func() { peerMu.Lock(); requests = nil; peerMu.Unlock() }
	traces := func() []trace { peerMu.Lock(); defer peerMu.Unlock(); return append([]trace(nil), requests...) }
	var host rsi.Host
	changed := 0
	maxEncoded := 0
	mutateBeforeRead := false
	denyStream := false
	var fenceChecks atomic.Int64
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		originalChanged := a.connectedApps.Changed
		a.connectedApps.Changed = func(c context.Context) error { changed++; return originalChanged(c) }
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			selectedHost := host
			if denyStream {
				base := *host.Store.(*rsistore.Store)
				prior := base.Fence
				base.Fence = func(c context.Context, tx *sql.Tx) error {
					if prior != nil {
						if err := prior(c, tx); err != nil {
							return err
						}
					}
					if fenceChecks.Add(1) >= 20 {
						return rsi.ErrRevoked
					}
					return nil
				}
				selectedHost.Store = &base
			}
			selected, err := a.appsAccountPoliciesWithHost(c, selectedHost)
			if err != nil {
				return connectedapps.AccountPolicies{}, err
			}
			if mutateBeforeRead {
				mutateBeforeRead = false
				state, e := a.store.LoadApps(c)
				if e != nil {
					t.Fatal(e)
				}
				if e := a.store.SaveApps(c, state, state.Revision); e != nil {
					t.Fatal(e)
				}
			}
			return selected, nil
		}
	}
	bind()
	save := func(rows []connectedapps.Connection) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:native-connect"
		state.UserID = "owner_fixture"
		state.Connections = rows
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	hashRows := func(rows []connectedapps.Connection) string {
		if rows == nil {
			rows = []connectedapps.Connection{}
		}
		raw, _ := json.Marshal(rows)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	errorName := func(err error) string {
		switch {
		case err == nil:
			return ""
		case errors.Is(err, connectedapps.ErrAliasRequired):
			return "alias_required"
		case errors.Is(err, connectedapps.ErrConflict):
			return "conflict"
		case errors.Is(err, connectedapps.ErrLinkExpired):
			return "link_expired"
		case errors.Is(err, connectedapps.ErrInvalid):
			return "invalid"
		case errors.Is(err, connectedapps.ErrUnavailable):
			return "unavailable"
		}
		t.Fatal("unexpected private error", err)
		return "unknown"
	}
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	var large []connectedapps.Connection
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			mode(false, false)
			save(nil)
			aliases := map[string]string{}
			for _, id := range fixture.Pending {
				link, err := a.connectedApps.Connect(ctx, "example", id, "")
				if err != nil {
					t.Fatal("real cache setup failed", err)
				}
				aliases[id] = link.ConnectionID
			}
			rows := append([]connectedapps.Connection(nil), fixture.Rows...)
			if fixture.Generated != nil {
				rows = make([]connectedapps.Connection, fixture.Generated.Count)
				for i := range rows {
					rows[i] = connectedapps.Connection{ID: fmt.Sprintf("original_%05d", i), Toolkit: "example", Name: "private original name", Alias: strings.Repeat("x", fixture.Generated.AliasRepeats) + fmt.Sprint(i), AccountID: "private original account", Status: "other"}
				}
				large = rows
			}
			reverse := map[string]string{}
			for i := range rows {
				if actual, ok := aliases[rows[i].ID]; ok {
					reverse[actual] = rows[i].ID
					rows[i].ID = actual
				}
			}
			var scope []contract.ConnectRow
			for _, row := range rows {
				if row.Toolkit == "example" {
					scope = append(scope, contract.ConnectRow{Alias: row.Alias})
				}
			}
			maxEncoded = max(maxEncoded, len(contract.EncodeConnectRows(scope)))
			before := save(rows)
			mode(fixture.NoAuth, fixture.FailSession)
			changed = 0
			link, err := a.connectedApps.Connect(ctx, "example", fixture.Alias, "")
			after, e := a.store.LoadApps(ctx)
			if e != nil {
				t.Fatal(e)
			}
			got := outcome{Error: errorName(err), Link: link, Requests: traces(), Saves: int(after.Revision - before.Revision), Changed: changed}
			if len(after.Connections) < len(rows) {
				t.Fatal("original rows dropped")
			}
			original := append([]connectedapps.Connection(nil), after.Connections[:len(rows)]...)
			for i := range original {
				if id, ok := reverse[original[i].ID]; ok {
					original[i].ID = id
				}
			}
			got.OriginalRowsSHA256 = hashRows(original)
			for _, row := range after.Connections[len(rows):] {
				row.ID = "$created"
				row.Alias = normalizeAlias(row.Alias)
				got.NewRows = append(got.NewRows, row)
			}
			if id, ok := reverse[got.Link.ConnectionID]; ok {
				got.Link.ConnectionID = id
			} else if got.Link.ConnectionID != "" {
				got.Link.ConnectionID = "$created"
			}
			if got.Link.ConnectionID != "" {
				if time.Until(link.ExpiresAt) <= 0 || time.Until(link.ExpiresAt) > connectedapps.AuthorizationTTL {
					t.Fatal("original authorization lifetime changed")
				}
				got.Link.ExpiresAt = time.Time{}
			}
			if !reflect.DeepEqual(got, fixture.Expected) {
				t.Fatalf("managed intent changed captured native behavior\ngot %+v\nwant %+v", got, fixture.Expected)
			}
		})
	}
	if maxEncoded <= rsiplugin.MaxInputBytes {
		t.Fatal("original alias corpus was silently capped", maxEncoded)
	}
	// Midstream revocation releases no plan and starts no original source request.
	save(large)
	mode(true, false)
	denyStream = true
	fenceChecks.Store(0)
	if _, err := a.connectedApps.Connect(ctx, "example", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(traces()) != 0 || fenceChecks.Load() < 20 {
		t.Fatal("revoked private stream continued into source", len(traces()), fenceChecks.Load(), err)
	}
	denyStream = false
	// State changed after the original selection, before launch: preserve cause.
	save(nil)
	clear()
	mutateBeforeRead = true
	offline := carrier + ".offline"
	if err := os.Rename(carrier, offline); err != nil {
		t.Fatal(err)
	}
	_, stale := a.connectedApps.Connect(ctx, "example", "", "")
	restore := os.Rename(offline, carrier)
	if restore != nil {
		t.Fatal(restore)
	}
	if !errors.Is(stale, connectedapps.ErrConflict) || len(traces()) != 0 {
		t.Fatal("stale intent launched or retried original source", len(traces()), stale)
	}
	// Actual source I/O occurs after the pure intent. Switching versions here must
	// retain the accepted old alias for this operation, then use the new default.
	save(nil)
	mode(true, false)
	seen, release := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(release) })
	defer releaseSource()
	peerMu.Lock()
	toolkitSeen, toolkitRelease = seen, release
	peerMu.Unlock()
	type connected struct {
		link composio.Link
		err  error
	}
	done := make(chan connected, 1)
	go func() { link, err := a.connectedApps.Connect(ctx, "example", "", ""); done <- connected{link, err} }()
	<-seen
	second, _ := activateAccountIntent(t, ctx, host, first, "1.1.0")
	releaseSource()
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].Alias != "personal" {
		t.Fatal("inflight intent followed new active graph", state, err)
	}
	save(nil)
	if _, err := a.connectedApps.Connect(ctx, "example", "", ""); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Alias != "primary" {
		t.Fatal("new alias policy was not actual business", state, err)
	}
	// The independently changed pending policy must affect the real cache lookup.
	save(nil)
	mode(false, false)
	older, err := a.connectedApps.Connect(ctx, "example", "older", "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := a.connectedApps.Connect(ctx, "example", "newer", "")
	if err != nil {
		t.Fatal(err)
	}
	clear()
	reused, err := a.connectedApps.Connect(ctx, "example", "", "")
	if err != nil || reused.ConnectionID != older.ConnectionID || reused.ConnectionID == newer.ConnectionID || len(traces()) != 0 {
		t.Fatal("independent first-pending policy did not reach original cache", reused, err)
	}
	server := &web.Server{Config: web.Config{TokenRef: "credential:account-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	owner := func(method, target, body string) (int, string) {
		request := httptest.NewRequest(method, "http://127.0.0.1:8080"+target, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "http://127.0.0.1:8080")
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
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
		server.Core, server.Apps = a, a.connectedApps
	}
	reopen()
	clear()
	if _, err := a.connectedApps.Connect(ctx, "example", "", ""); !errors.Is(err, connectedapps.ErrLinkExpired) || len(traces()) != 0 {
		t.Fatal("cold missing authorization cache silently created another source", err)
	}
	save(nil)
	mode(true, false)
	if code, body := owner("POST", "/apps/configure", url.Values{"action": {"connect"}, "toolkit": {"example"}}.Encode()); code != 200 {
		t.Fatal("cold owner HTTP connect failed", code, body)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].Alias != "primary" {
		t.Fatal("cold version lost managed alias", state, err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "accounts-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original connect intent"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "accounts-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "accounts-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	save(nil)
	if _, err := a.connectedApps.Connect(ctx, "example", "", ""); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Alias != "personal" {
		t.Fatal("cold rollback lost original alias", state, err)
	}
	actualPublic := state.Connections[0]
	// Directory activation and subsequent removal must not disable the separate
	// account policy. Combine the actual independently-owned functional policies.
	dirGraph, _ := activateEditorialVersion(t, ctx, host, first, "1.0.0")
	accountGraph, err := host.Store.Graph(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	directoryGraph, err := host.Store.Graph(ctx, dirGraph)
	if err != nil {
		t.Fatal(err)
	}
	bindings := append(append([]rsiplugin.Binding(nil), accountGraph.Graph.Bindings...), directoryGraph.Graph.Bindings...)
	combined, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, "editorial-owner-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	accountPolicy, err := host.Store.Policy(ctx, "accounts-owner-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "accounts-directory-owner"
	policy.AllowedContracts = append(policy.AllowedContracts, accountPolicy.AllowedContracts...)
	policy.Cases = append(policy.Cases, accountPolicy.Cases...)
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "accounts-directory", Baseline: dirGraph, Target: combined.Revision, PolicyID: policy.ID, Goal: "Keep directory and account policy independently composed"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "accounts-directory")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "accounts-directory", Expected: dirGraph, Target: combined.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "directory-remove-only", Baseline: combined.Revision, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Remove public directory while retaining account control"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "directory-remove-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "directory-remove-only", Expected: combined.Revision, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	save(nil)
	clear()
	if code, body := owner("POST", "/apps/configure", url.Values{"action": {"connect"}, "toolkit": {"example"}}.Encode()); code != 200 {
		t.Fatal("directory removal disabled account control", code, body)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].Alias != "personal" {
		t.Fatal("directory removal caused native or missing intent", state, err)
	}
	// Retain an actual private pending connection for stop/reconnect protection.
	save(nil)
	mode(false, false)
	keeper, err := a.connectedApps.Connect(ctx, "example", "keeper", "")
	if err != nil {
		t.Fatal(err)
	}
	keeperState, err := a.store.LoadApps(ctx)
	if err != nil || len(keeperState.Connections) != 1 {
		t.Fatal(err)
	}
	actualPending := keeperState.Connections[0]
	if keeper.ConnectionID != actualPending.ID {
		t.Fatal("original pending identity changed")
	}
	mode(true, false)
	third, _ := activateAccountIntent(t, ctx, host, first, "2.0.0")
	for _, count := range []int{7, 8, 9} {
		rows := make([]connectedapps.Connection, count)
		for i := range rows {
			rows[i] = connectedapps.Connection{ID: fmt.Sprint(i), Toolkit: "example", Alias: fmt.Sprintf("unused%d", i), Status: "other"}
		}
		if count == 9 {
			rows[0].Alias = "work"
			rows[0].Status = "pending"
		}
		before := save(rows)
		clear()
		if _, err := a.connectedApps.Connect(ctx, "example", "work", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(traces()) != 0 {
			t.Fatal("forged intent reached source or native fallback", count, len(traces()), err)
		}
		after, err := a.store.LoadApps(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("forged intent changed original state", count, err)
		}
	}
	save([]connectedapps.Connection{actualPublic, actualPending})
	clear()
	if err := host.Store.Quarantine(ctx, third); err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.Connect(ctx, "example", "new_account", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(traces()) != 0 {
		t.Fatal("quarantine restored native intent", err)
	}
	if _, err := a.connectedApps.Reconnect(ctx, actualPublic.ID); !errors.Is(err, connectedapps.ErrDenied) {
		t.Fatal("active original reconnect semantics changed", err)
	}
	beforeReconnect, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.Reconnect(ctx, actualPending.ID); !errors.Is(err, connectedapps.ErrUnavailable) || len(traces()) != 0 {
		t.Fatal("stopped policy reached reconnect source", err)
	}
	afterReconnect, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(beforeReconnect, afterReconnect) {
		t.Fatal("unavailable policy closed original pending connection", err)
	}
	if err := a.connectedApps.Disconnect(ctx, actualPublic.ID); err != nil {
		t.Fatal("quarantined policy blocked owner revocation", err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "disconnected" || !state.Connections[0].RevocationConfirmed {
		t.Fatal("original local closure was lost", state, err)
	}
	if _, err := host.Store.Deactivate(ctx, "accounts-disable", third); err != nil {
		t.Fatal(err)
	}
	reopen()
	save(nil)
	clear()
	if _, err := a.connectedApps.Connect(ctx, "example", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || len(traces()) != 0 {
		t.Fatal("cold deactivation restored native default", err)
	}
}
