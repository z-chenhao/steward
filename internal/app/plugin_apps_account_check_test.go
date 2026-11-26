package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

type accountCheckFixture struct {
	Name, Previous, SourceStatus                                                                          string
	Disabled, Public, NoAuth, WrongID, WrongUser, WrongToolkit, SaveFail, ChangedFail, Missing, Expansion bool
	HTTPStatus                                                                                            int
	Expected                                                                                              struct {
		Error, Status            string
		Revision                 int64
		Saves, Changed, Requests int
		Cached                   bool
		NormalizedStatusBytes    int
		NormalizedStatusSHA256   string
	}
}

func TestAppsAccountCheckActualOriginalSourceVersionColdAndFences(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-check-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []accountCheckFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var mu sync.Mutex
	var active accountCheckFixture
	seeding := false
	requests := 0
	var entered, release chan struct{}
	var afterSource func()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		f, seed := active, seeding
		seen, proceed, action := entered, release, afterSource
		if !seed {
			requests++
			entered, release, afterSource = nil, nil, nil
		}
		mu.Unlock()
		if seed {
			switch r.URL.Path {
			case "/toolkits/example":
				_, _ = w.Write([]byte(`{"slug":"example","name":"Original"}`))
			case "/auth_configs/ac_original":
				_, _ = w.Write([]byte(`{"id":"ac_original","toolkit":{"slug":"example"},"status":"ENABLED","is_composio_managed":false,"is_enabled_for_tool_router":true}`))
			case "/tool_router/session":
				_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
			case "/tool_router/session/session_original/link":
				_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_original"}`))
			default:
				t.Error("unexpected seed source", r.URL)
				http.NotFound(w, r)
			}
			return
		}
		if seen != nil {
			close(seen)
			<-proceed
		}
		if action != nil {
			action()
		}
		if f.HTTPStatus != 0 {
			w.WriteHeader(f.HTTPStatus)
			return
		}
		toolkit := "example"
		if f.WrongToolkit {
			toolkit = "foreign"
		}
		if f.Public {
			if r.URL.Path != "/toolkits/example" {
				t.Error("wrong public source", r.URL)
			}
			_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: toolkit, NoAuth: f.NoAuth})
			return
		}
		if r.URL.Path != "/connected_accounts/account_original" {
			t.Error("wrong account source", r.URL)
		}
		id, user := "account_original", "owner"
		if f.WrongID {
			id = "foreign"
		}
		if f.WrongUser {
			user = "foreign"
		}
		status := f.SourceStatus
		if f.Expansion {
			status = strings.Repeat("q", 1<<20)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": user, "status": status, "is_disabled": f.Disabled, "toolkit": map[string]string{"slug": toolkit}, "data": map[string]string{"access_token": "excluded-private"}})
	}))
	defer peer.Close()
	var host rsi.Host
	var store *sessionFlowStore
	changed := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		store = &sessionFlowStore{Store: a.store}
		a.connectedApps.Store = store
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.Changed = func(context.Context) error {
			changed++
			mu.Lock()
			fail := active.ChangedFail
			mu.Unlock()
			if fail {
				return errors.New("private callback")
			}
			return nil
		}
	}
	bind()
	setup := func(f accountCheckFixture) (connectedapps.State, composio.Link) {
		t.Helper()
		key := "synthetic-check-key"
		if f.Expansion {
			key = "q"
		}
		if err := secrets.Put(ctx, "credential:fixture", []byte(key)); err != nil {
			t.Fatal(err)
		}
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.KeyRef = "credential:fixture"
		state.UserID = "owner"
		state.AuthConfigs = map[string]string{"example": "ac_original"}
		state.Connections = nil
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		store.fail = false
		mu.Lock()
		seeding = true
		active = f
		mu.Unlock()
		link, err := a.connectedApps.Connect(ctx, "example", "work", "")
		if err != nil {
			t.Fatal("actual accepted cache seed", err)
		}
		mu.Lock()
		seeding = false
		requests = 0
		mu.Unlock()
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.Connections[0].Status = f.Previous
		state.Connections[0].Public = f.Public
		if f.Missing {
			state.Connections = nil
		}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		store.fail = f.SaveFail
		store.saves = 0
		changed = 0
		return state, link
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return requests }
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
	first, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			before, link := setup(f)
			err := a.connectedApps.Check(ctx, link.ConnectionID)
			state, loadErr := a.store.LoadApps(ctx)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			status := ""
			if len(state.Connections) != 0 {
				status = state.Connections[0].Status
			}
			if sessionFlowError(err) != f.Expected.Error || status != f.Expected.Status || state.Revision-before.Revision != f.Expected.Revision-7 || store.saves != f.Expected.Saves || changed != f.Expected.Changed || count() != f.Expected.Requests {
				t.Fatal("original check outcome changed", sessionFlowError(err), status, state.Revision-before.Revision, store.saves, changed, count(), f.Expected)
			}
			if status == "pending" {
				resumed, resumeErr := a.connectedApps.Continue(ctx, link.ConnectionID)
				if !f.Expected.Cached || resumeErr != nil || resumed != link {
					t.Fatal("original pending cache changed", resumed, resumeErr)
				}
			}
			if f.Expansion {
				account, err := (composio.Client{Endpoint: peer.URL, Secrets: secrets, KeyRef: "credential:fixture"}).Account(ctx, "account_original")
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256([]byte(account.Status))
				if len(account.Status) != f.Expected.NormalizedStatusBytes || hex.EncodeToString(hash[:]) != f.Expected.NormalizedStatusSHA256 {
					t.Fatal("original decoded source narrowed")
				}
			}
		})
	}
	// A real later Check can restore pending, but it cannot recreate an already
	// discarded accepted link. No fixture repair or silent session POST intervenes.
	_, link := setup(fixtures[0])
	if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	active.SourceStatus = "INITIATED"
	mu.Unlock()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.Continue(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrLinkExpired) {
		t.Fatal("restored pending silently rebuilt old link", err)
	}
	f := fixtures[5]
	_, link = setup(f)
	seen, proceed := make(chan struct{}), make(chan struct{})
	releaseSource := sync.OnceFunc(func() { close(proceed) })
	defer releaseSource()
	mu.Lock()
	entered, release = seen, proceed
	mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.connectedApps.Check(ctx, link.ConnectionID) }()
	<-seen
	second, _ := activateAccountIntent(t, ctx, host, first, "1.4.0")
	releaseSource()
	if err := <-done; err != nil {
		t.Fatal("inflight status policy reselected", err)
	}
	state, err := a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "pending" {
		t.Fatal("old inflight interpretation changed", state, err)
	}
	_, link = setup(f)
	if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "expired" {
		t.Fatal("new status decision did not change", state, err)
	}
	binding := connectedapps.BindingHash(state, state.Connections[0])
	reopen()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "expired" || connectedapps.BindingHash(state, state.Connections[0]) != binding {
		t.Fatal("cold version/binding lost", state, err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "check-rollback", Baseline: second, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original source status policy"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "check-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "check-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || state.Connections[0].Status != "pending" || connectedapps.BindingHash(state, state.Connections[0]) != binding {
		t.Fatal("cold rollback did not restore original binding/status", state, err)
	}
	_, link = setup(fixtures[0])
	mu.Lock()
	afterSource = func() {
		v, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if err := a.store.SaveApps(ctx, v, v.Revision); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if err := os.Rename(carrier, carrier+".held"); err != nil {
		t.Fatal(err)
	}
	checkErr := a.connectedApps.Check(ctx, link.ConnectionID)
	if err := os.Rename(carrier+".held", carrier); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(checkErr, connectedapps.ErrConflict) || store.saves != 0 || changed != 0 || count() != 1 {
		t.Fatal("stale source reached process/write/callback", checkErr, store.saves, changed, count())
	}
	baseline := first
	for i, version := range []string{"4.0.0", "4.1.0", "4.2.0", "4.3.0"} {
		graph, _ := activateAccountIntent(t, ctx, host, baseline, version)
		baseline = graph
		f := fixtures[3]
		if i == 1 {
			f = fixtures[9]
		}
		if i == 2 {
			f = fixtures[0]
		}
		if i == 3 {
			f = fixtures[19]
		}
		before, link := setup(f)
		err := a.connectedApps.Check(ctx, link.ConnectionID)
		now, loadErr := a.store.LoadApps(ctx)
		if !errors.Is(err, connectedapps.ErrUnavailable) || loadErr != nil || now.Revision != before.Revision || now.Connections[0].Status != f.Previous || store.saves != 0 || changed != 0 || count() != 1 {
			t.Fatal("forged status escaped source proof", version, err, now, store.saves, changed, count())
		}
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "check-restore", Baseline: baseline, Target: first, PolicyID: "accounts-owner-1.0.0", Goal: "Restore original for private read fence"}); err != nil {
		t.Fatal(err)
	}
	decision, err = host.Verify(ctx, "check-restore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "check-restore", Expected: baseline, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	_, link = setup(fixtures[0])
	mu.Lock()
	afterSource = func() {
		if err := host.Store.Quarantine(ctx, first); err != nil {
			t.Error(err)
		}
	}
	mu.Unlock()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) || store.saves != 0 || changed != 0 || count() != 1 {
		t.Fatal("late quarantine accepted source status", err, store.saves, changed, count())
	}
	mu.Lock()
	requests = 0
	mu.Unlock()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) || count() != 0 {
		t.Fatal("quarantine restored native check", err, count())
	}
	// Owner local closure is independent of the status candidate.
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Connections[0].Public = true
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.connectedApps.Disconnect(ctx, link.ConnectionID); err != nil {
		t.Fatal("candidate blocked owner revoke", err)
	}
	if _, err := host.Store.Deactivate(ctx, "check-disable", first); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Connections[0].Status = "pending"
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	reopen()
	mu.Lock()
	requests = 0
	mu.Unlock()
	if err := a.connectedApps.Check(ctx, link.ConnectionID); !errors.Is(err, connectedapps.ErrUnavailable) || count() != 0 {
		t.Fatal("cold disabled check restored native", err, count())
	}
}
