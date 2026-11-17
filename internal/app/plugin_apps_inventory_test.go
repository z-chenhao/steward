package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	xhtml "golang.org/x/net/html"
)

func TestAppsDirectoryActualInventoryOriginalReferencesVersionAndCold(t *testing.T) {
	ctx := context.Background()
	type entry struct{ Slug, Name string }
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-inventory-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                   string
		Rows                   []connectedapps.Connection
		ExpectedInventory      []entry
		ExpectedConnectedOrder []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-inventory-owner-token-24-characters"
	if err := secrets.Put(ctx, "credential:inventory-owner", []byte(token)); err != nil {
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
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	host := a.Plugins()
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.ReadEditorial = func(c context.Context, q contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			return a.readAppsEditorialWithHost(c, host, q)
		}
	}
	bind()
	server := &web.Server{Config: web.Config{TokenRef: "credential:inventory-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	get := func(path ...string) (int, string) {
		target := "/settings/apps?view=connected"
		if len(path) > 0 {
			target = path[0]
		}
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080"+target, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	attr := func(n *xhtml.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	var text func(*xhtml.Node) string
	text = func(n *xhtml.Node) string {
		if n.Type == xhtml.TextNode {
			return n.Data
		}
		var out string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out += text(c)
		}
		return out
	}
	parse := func(body string) ([]entry, []string) {
		t.Helper()
		doc, err := xhtml.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var sidebar []entry
		var tiles []string
		var walk func(*xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode && n.Data == "nav" && strings.Contains(attr(n, "class"), "apps-connections") {
				for a := n.FirstChild; a != nil; a = a.NextSibling {
					if a.Type != xhtml.ElementNode || a.Data != "a" {
						continue
					}
					name := ""
					for span := a.FirstChild; span != nil; span = span.NextSibling {
						if span.Type == xhtml.ElementNode && span.Data == "span" && attr(span, "class") == "" {
							name = text(span)
							break
						}
					}
					sidebar = append(sidebar, entry{Slug: attr(a, "data-app-open"), Name: name})
				}
			}
			if n.Type == xhtml.ElementNode && n.Data == "article" {
				for _, a := range n.Attr {
					if a.Key == "data-app-tile" {
						tiles = append(tiles, a.Val)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		return sidebar, tiles
	}
	save := func(rows []connectedapps.Connection) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
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
	selection := func() connectedapps.DirectorySelection {
		t.Helper()
		s, err := a.connectedApps.Directory(ctx, contract.Query{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, _ := activateEditorialVersion(t, ctx, host, "", "1.0.0")
	for _, fixture := range cases {
		t.Run(fixture.Name, func(t *testing.T) {
			state := save(fixture.Rows)
			indices, err := selection().Inventory(ctx, state)
			if err != nil {
				t.Fatal(err)
			}
			last := -1
			seen := map[string]bool{}
			for _, index := range indices.Rows {
				if index <= last || index < 0 || index >= len(state.Connections) || seen[state.Connections[index].Toolkit] {
					t.Fatal("original reference/identity projection failed", indices)
				}
				last = index
				seen[state.Connections[index].Toolkit] = true
			}
			code, body := get("/settings/apps")
			if code != 200 {
				t.Fatal("actual inventory page failed", code, body)
			}
			sidebar, _ := parse(body)
			code, body = get()
			if code != 200 {
				t.Fatal("connected page failed", code)
			}
			_, tiles := parse(body)
			if !reflect.DeepEqual(sidebar, fixture.ExpectedInventory) || !reflect.DeepEqual(tiles, fixture.ExpectedConnectedOrder) {
				t.Fatal("captured native inventory or two orders changed", sidebar, fixture.ExpectedInventory, tiles, fixture.ExpectedConnectedOrder)
			}
		})
	}
	rows := make([]connectedapps.Connection, contract.CardStateWindow+3)
	for i := range rows {
		rows[i] = connectedapps.Connection{Toolkit: "example", Name: "Hidden original middle", ID: "private id", Alias: "private alias", AccountID: "private account", Status: "active"}
	}
	rows[0].Name = "Original first representative"
	rows[len(rows)-1].Name = "Independent last representative"
	state := save(rows)
	frozen := selection()
	if indices, err := frozen.Inventory(ctx, state); err != nil || !reflect.DeepEqual(indices.Rows, []int{0}) {
		t.Fatal("complete original representative changed", indices, err)
	}
	second, _ := activateEditorialVersion(t, ctx, host, first, "1.2.0")
	indices, err := selection().Inventory(ctx, state)
	if err != nil || !reflect.DeepEqual(indices.Rows, []int{len(rows) - 1}) {
		t.Fatal("candidate did not change actual representative", indices, err)
	}
	if indices, err := frozen.Inventory(ctx, state); err != nil || !reflect.DeepEqual(indices.Rows, []int{0}) {
		t.Fatal("frozen inventory followed active version", indices, err)
	}
	// Small source produces a concrete visible name change without sending it to the candidate.
	save([]connectedapps.Connection{rows[0], rows[len(rows)-1]})
	code, body := get()
	sidebar, _ := parse(body)
	if code != 200 || !reflect.DeepEqual(sidebar, []entry{{Slug: "example", Name: "Independent last representative"}}) {
		t.Fatal("actual owner HTML ignored selected original name", code, sidebar)
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
	code, body = get()
	sidebar, _ = parse(body)
	if code != 200 || !reflect.DeepEqual(sidebar, []entry{{Slug: "example", Name: "Independent last representative"}}) {
		t.Fatal("cold selected representative changed", code, sidebar)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "inventory-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore captured first eligible original row"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "inventory-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "inventory-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	code, body = get()
	sidebar, _ = parse(body)
	if code != 200 || !reflect.DeepEqual(sidebar, []entry{{Slug: "example", Name: "Original first representative"}}) {
		t.Fatal("cold rollback changed original representative", code, sidebar)
	}
	third, _ := activateEditorialVersion(t, ctx, host, first, "2.0.0")
	invalid := make([]connectedapps.Connection, 7)
	for i := range invalid {
		invalid[i] = connectedapps.Connection{Toolkit: "example", Name: "guarded inventory name", Status: "other"}
	}
	state = save(invalid)
	if indices, err := selection().Inventory(ctx, state); !errors.Is(err, connectedapps.ErrUnavailable) || indices.Rows != nil {
		t.Fatal("bad plan restored native selection", indices, err)
	}
	if code, body := get(); code == 200 || strings.Contains(body, "guarded inventory name") {
		t.Fatal("managed inventory failure released fallback/private body", code)
	}
	state = save(rows)
	retained := selection()
	if err := host.Store.Quarantine(ctx, third); err != nil {
		t.Fatal(err)
	}
	if indices, err := retained.Inventory(ctx, state); !errors.Is(err, connectedapps.ErrUnavailable) || indices.Rows != nil {
		t.Fatal("quarantine restored native representative", indices, err)
	}
}
