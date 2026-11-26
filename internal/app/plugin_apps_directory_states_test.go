package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"
)

func TestAppsDirectoryActualCardStatesOriginalRowsVersionAndRevisionFence(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-inventory-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name         string
		Rows         []connectedapps.Connection
		ExpectedCard connectedapps.CardState
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
	const ownerToken = "synthetic-card-owner-token-long-enough"
	if err := secrets.Put(ctx, "credential:card-owner", []byte(ownerToken)); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, "credential:card-source", []byte("synthetic-card-source-key")); err != nil {
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
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/toolkits/example" || r.Header.Get("x-api-key") != "synthetic-card-source-key" {
			t.Error("original detail authority changed")
			http.Error(w, "invalid", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: "example", Name: "Original toolkit"})
	}))
	defer peer.Close()
	server := &web.Server{Config: web.Config{TokenRef: "credential:card-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	get := func() (int, string) {
		t.Helper()
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps?app=example", nil)
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	assertAddAccount := func(hidden bool) {
		t.Helper()
		code, body := get()
		if code != 200 {
			t.Fatal("actual state view failed", code, body)
		}
		start := strings.Index(body, `<details class="app-add-account" data-app-add-account`)
		if start < 0 {
			t.Fatal("original account control missing")
		}
		tag := body[start:]
		tag = tag[:strings.Index(tag, ">")]
		if strings.Contains(tag, "hidden") != hidden {
			t.Fatal("actual page ignored selected original active/public row", tag, hidden)
		}
	}

	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	var offsets []int
	mutateAfterWindow := false
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadEditorial = func(c context.Context, q contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			value, managed, methods, err := a.readAppsEditorialWithHost(c, host, q)
			if methods != nil {
				original := methods.CardStates
				methods.CardStates = func(c context.Context, revision int64, request contract.CardStateRequest, data []byte) (contract.CardStatePlan, error) {
					offsets = append(offsets, request.Offset)
					plan, err := original(c, revision, request, data)
					if mutateAfterWindow && err == nil && request.Offset == 0 {
						current, e := a.store.LoadApps(c)
						if e != nil {
							t.Fatal(e)
						}
						current.Connections = nil
						if e := a.store.SaveApps(c, current, current.Revision); e != nil {
							t.Fatal(e)
						}
						mutateAfterWindow = false
					}
					return plan, err
				}
			}
			return value, managed, methods, err
		}
	}
	bind()
	first, _ := activateEditorialVersion(t, ctx, host, "", "1.0.0")
	save := func(rows []connectedapps.Connection) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.Connections = rows
		state.KeyRef = "credential:card-source"
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	selection := func() connectedapps.DirectorySelection {
		t.Helper()
		s, err := a.connectedApps.Directory(ctx, contract.Query{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			// Controlled persisted rows are original evidence inputs, not account-creation proof.
			state := save(fixture.Rows)
			actual, err := readCardStates(ctx, state, []string{"example"}, selection())
			if err != nil || len(actual) != 1 || actual[0] != fixture.ExpectedCard {
				t.Fatal("original card facts changed", actual, fixture.ExpectedCard, err)
			}
		})
	}

	// A domain-specific revision fence must compose, never replace, the caller's
	// already installed Host fence.
	currentState := save([]connectedapps.Connection{{Toolkit: "example", Status: "active"}})
	inherited := *host.Store.(*rsistore.Store)
	var deny atomic.Bool
	inherited.Fence = func(context.Context, *sql.Tx) error {
		if deny.Load() {
			return rsi.ErrRevoked
		}
		return nil
	}
	guardedHost := host
	guardedHost.Store = &inherited
	_, _, guardedMethods, err := a.readAppsEditorialWithHost(ctx, guardedHost, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	deny.Store(true)
	if _, err := guardedMethods.CardStates(ctx, currentState.Revision, contract.CardStateRequest{Count: 1}, []byte{contract.FactActive}); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("private state fence replaced original Host fence", err)
	}
	rows := make([]connectedapps.Connection, contract.CardStateWindow+3)
	for i := range rows {
		rows[i] = connectedapps.Connection{ID: "private-id", Toolkit: "example", Name: "private name", Alias: "private alias", AccountID: "private account", Status: "active"}
	}
	rows[len(rows)-1].Public = true
	state := save(rows)
	frozen := selection()
	offsets = nil
	actual, err := readCardStates(ctx, state, []string{"example", "example", "unconnected"}, frozen)
	if err != nil || len(actual) != 3 || actual[0] != (connectedapps.CardState{Managed: true, Connected: true, PublicActive: true}) || actual[1] != actual[0] || actual[2] != (connectedapps.CardState{}) || !reflect.DeepEqual(offsets, []int{0, 65536}) {
		t.Fatal("complete private windows/dedup/no-row proof failed", actual, offsets, err)
	}

	save([]connectedapps.Connection{{ID: "public", Toolkit: "example", Name: "Public fixture", Status: "active", Public: true}, {ID: "private", Toolkit: "example", Name: "Private fixture", Status: "active"}})
	assertAddAccount(false)
	state = save(rows)
	second, _ := activateEditorialVersion(t, ctx, host, first, "1.1.0")
	actual, err = readCardStates(ctx, state, []string{"example"}, selection())
	if err != nil || len(actual) != 1 || actual[0].PublicActive || !actual[0].Connected {
		t.Fatal("independent first-active policy ignored", actual, err)
	}

	save([]connectedapps.Connection{{ID: "public", Toolkit: "example", Name: "Public fixture", Status: "active", Public: true}, {ID: "private", Toolkit: "example", Name: "Private fixture", Status: "active"}})
	assertAddAccount(true)
	state = save(rows)
	original, err := readCardStates(ctx, state, []string{"example"}, frozen)
	if err != nil || len(original) != 1 || !original[0].PublicActive {
		t.Fatal("frozen state policy followed active graph", original, err)
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
	actual, err = readCardStates(ctx, state, []string{"example"}, selection())
	if err != nil || len(actual) != 1 || actual[0].PublicActive {
		t.Fatal("cold policy selection changed", actual, err)
	}
	// Acquire a real bound method before changing the original state. Removing the
	// owned carrier proves stale facts fail with the revision cause before launch.
	_, _, methods, err := a.readAppsEditorialWithHost(ctx, host, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	save(nil)
	offline := carrier + ".offline"
	if err := os.Rename(carrier, offline); err != nil {
		t.Fatal(err)
	}
	plan, staleErr := methods.CardStates(ctx, state.Revision, contract.CardStateRequest{Count: 1}, []byte{contract.FactActive})
	restoreErr := os.Rename(offline, carrier)
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if !errors.Is(staleErr, connectedapps.ErrConflict) || plan.Active != nil {
		t.Fatal("stale state crossed private fence or needed an executable", plan, staleErr)
	}
	// A changed original state between real accepted windows discards the prefix.
	state = save(rows)
	offsets = nil
	mutateAfterWindow = true
	actual, err = readCardStates(ctx, state, []string{"example"}, selection())
	if !errors.Is(err, connectedapps.ErrUnavailable) || actual != nil || !reflect.DeepEqual(offsets, []int{0, 65536}) {
		t.Fatal("late original-state change released private stale flags", actual, offsets, err)
	}
	// Preserve real original rows; only the selected policy changes on rollback.
	state = save(rows)
	if err := host.Propose(ctx, rsi.Candidate{ID: "card-state-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore original proof-backed card state references"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "card-state-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "card-state-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	actual, err = readCardStates(ctx, state, []string{"example"}, selection())
	if err != nil || len(actual) != 1 || !actual[0].PublicActive {
		t.Fatal("cold rollback did not restore original last-active reference", actual, err)
	}
	third, _ := activateEditorialVersion(t, ctx, host, first, "2.0.0")
	for _, count := range []int{7, 8, 9} {
		invalid := make([]connectedapps.Connection, count)
		for i := range invalid {
			invalid[i] = connectedapps.Connection{Toolkit: "example", Name: "guarded-private-name", Alias: "guarded-private-alias", ID: "guarded-private-id", AccountID: "guarded-private-account", Status: "other"}
		}
		state = save(invalid)
		actual, err = readCardStates(ctx, state, []string{"example"}, selection())
		if !errors.Is(err, connectedapps.ErrUnavailable) || actual != nil {
			t.Fatal("forged row/state reference released", count, actual, err)
		}
		if code, body := get(); code == 200 || strings.Contains(body, "guarded-private-name") {
			t.Fatal("invalid state plan released a private page", count, code)
		}
	}
	state = save(rows)
	retained := selection()
	if err := host.Store.Quarantine(ctx, third); err != nil {
		t.Fatal(err)
	}
	if actual, err := readCardStates(ctx, state, []string{"example"}, retained); !errors.Is(err, connectedapps.ErrUnavailable) || actual != nil {
		t.Fatal("quarantine restored native flags", actual, err)
	}
}
