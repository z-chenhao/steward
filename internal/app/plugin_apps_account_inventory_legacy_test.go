package app

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	directory "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppsAccountInventoryOriginalProfileRollbackAndFirstInstall(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "retained-original"
		if fresh {
			name = "first-installed-original"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			cfg, e := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
			if e != nil {
				t.Fatal(e)
			}
			secrets := memory.New()
			a, e := OpenRuntimeWithSecrets(ctx, cfg, secrets)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			a.eventsRequired.Store(false)
			if _, e = a.BindChannel(ctx, "web", "browser", "local", true, 0); e != nil {
				t.Fatal(e)
			}
			const token = "synthetic-original-inventory-owner-token"
			if e = secrets.Put(ctx, "credential:original-inventory-owner", []byte(token)); e != nil {
				t.Fatal(e)
			}
			carrier := filepath.Join(t.TempDir(), "steward")
			if out, e := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); e != nil {
				t.Fatal(string(out), e)
			}
			host := a.Plugins()
			host.Catalog.Launcher = carrier
			bind := func() {
				a.connectedApps.ReadInventory = func(c context.Context) (connectedapps.AccountInventoryPolicy, error) {
					return a.appsAccountInventoryWithHost(c, host)
				}
				a.connectedApps.ReadEditorial = func(context.Context, directory.Query) (directory.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
					return directory.Catalogue{}, true, nil, connectedapps.ErrUnavailable
				}
			}
			bind()
			page := func() int {
				t.Helper()
				server := &web.Server{Config: web.Config{TokenRef: "credential:original-inventory-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
				request := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps?view=connected", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code == 200 && (!strings.Contains(response.Body.String(), "Alpha") || !strings.Contains(response.Body.String(), "Zulu")) {
					t.Fatal("historical account page lost original display facts")
				}
				return response.Code
			}

			first, firstPolicy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
			original := ""
			if !fresh {
				original, _ = activateAccountIntent(t, ctx, host, "", "1.0.0")
			}
			activateInterface(t, ctx, host, original, first, firstPolicy, "inventory-original-upgrade")
			if fresh {
				original, _ = activateAccountIntent(t, ctx, host, first, "1.0.0")
			} else {
				activateInterface(t, ctx, host, first, original, "accounts-owner-1.0.0", "inventory-original-rollback")
			}
			state, e := a.store.LoadApps(ctx)
			if e != nil {
				t.Fatal(e)
			}
			state.Connections = []connectedapps.Connection{{Toolkit: "z", Name: "Zulu", Status: "pending"}, {Toolkit: "a", Name: "Alpha", Status: "active", Public: true}, {Toolkit: "gone", Name: "Gone", Status: "disconnected", RevocationConfirmed: true}}
			if e = a.store.SaveApps(ctx, state, state.Revision); e != nil {
				t.Fatal(e)
			}
			state, e = a.store.LoadApps(ctx)
			if e != nil {
				t.Fatal(e)
			}
			check := func(p connectedapps.AccountInventoryPolicy) {
				t.Helper()
				got, err := p.Project(ctx, state, true)
				if err != nil || len(got.Rows) != 2 || got.Rows[0] != 1 || got.Rows[1] != 0 {
					t.Fatal("lost original profile projection", got.Rows, err)
				}
				flags := got.States([]string{"a", "z"})
				if !flags[0].Connected || !flags[0].PublicActive || !flags[1].Pending {
					t.Fatal("changed original state facts", flags)
				}
			}
			p, e := a.appsAccountInventoryWithHost(ctx, host)
			if e != nil {
				t.Fatal("legitimate original rollback denied", e)
			}
			check(p)
			if code := page(); code != 200 {
				t.Fatal("original rollback page unavailable", code)
			}
			if e = a.store.SaveApps(ctx, state, state.Revision); e != nil {
				t.Fatal(e)
			}
			if _, e = p.Project(ctx, state, true); !errors.Is(e, connectedapps.ErrConflict) {
				t.Fatal("historical profile released stale state", e)
			}
			state, e = a.store.LoadApps(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
			if e != nil {
				t.Fatal(e)
			}
			a.eventsRequired.Store(false)
			host = a.Plugins()
			host.Catalog.Launcher = carrier
			bind()
			// First actual dependent read after recovery; no reselect or state repair.
			p, e = a.appsAccountInventoryWithHost(ctx, host)
			if e != nil {
				t.Fatal(e)
			}
			check(p)
			if code := page(); code != 200 {
				t.Fatal("first recovered historical page unavailable", code)
			}
			frozen, e := a.appsAccountInventoryForGraph(ctx, host, original)
			if e != nil {
				t.Fatal(e)
			}
			// Another seven-service producer is not the captured original profile merely
			// because it omits Inventory. A missing current service stays unavailable.
			activateAccountIntent(t, ctx, host, original, "1.10.0")
			if _, e = a.appsAccountInventoryWithHost(ctx, host); e == nil {
				t.Fatal("missing new inventory silently selected native interpretation")
			}
			check(frozen)
			if code := page(); code == 200 {
				t.Fatal("missing current service restored native account page")
			}
			if e = host.Store.Quarantine(ctx, original); e != nil {
				t.Fatal(e)
			}
			if _, e = frozen.Project(ctx, state, true); e == nil {
				t.Fatal("historical interpreter bypassed graph revocation")
			}
		})
	}
}
