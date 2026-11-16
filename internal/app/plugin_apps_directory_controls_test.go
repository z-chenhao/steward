package app

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsDirectoryControlsActualVersionsQueriesAndColdReferences(t *testing.T) {
	testAppsViewResourceVersions(t, false)
}

func TestAppsPagesActualVersionsAndColdReferences(t *testing.T) {
	testAppsViewResourceVersions(t, true)
}

// Both real resource generations share the original App/SQLite/source fixture.
func testAppsViewResourceVersions(t *testing.T, pages bool) {
	oldVersion, firstVersion, secondVersion, badVersion := "1.15.0", "1.17.0", "1.18.0", "4.9.0"
	boundary := 6
	if pages {
		oldVersion, firstVersion, secondVersion, badVersion = "1.17.0", "1.19.0", "1.20.0", "4.11.0"
		boundary = 7
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-directory-controls-owner-atleast24"
	for k, v := range map[string]string{"credential:directory-owner": token, "env:COMPOSIO_API_KEY": "synthetic-directory-project"} {
		if err := secrets.Put(ctx, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
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
	const search = "mail & jobs"
	const category = "communication"
	const next = `next opaque /<&" 中文`
	var mu sync.Mutex
	var requests []url.Values
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pages && r.Method == "GET" && r.URL.Path == "/toolkits/gmail" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail","meta":{"description":"Read mail"}}`))
			return
		}
		if r.Method != "GET" || r.URL.Path != "/toolkits" {
			t.Error("unexpected original source", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		requests = append(requests, q)
		mu.Unlock()
		item := composio.Toolkit{Slug: "gmail", Name: "Gmail"}
		item.Meta.Description = "Read mail"
		item.Meta.Categories = []composio.Category{{ID: category, Name: "Communication"}}
		cursor := next
		if q.Get("cursor") == next {
			cursor = ""
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(composio.Catalog{Items: []composio.Toolkit{item}, NextCursor: cursor})
	}))
	defer peer.Close()
	var host rsi.Host
	var server *web.Server
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Changed = nil
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		server = &web.Server{Config: web.Config{TokenRef: "credential:directory-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, ref, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, ref, locale)
		}}
	}
	bind()
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "env:COMPOSIO_API_KEY"
	state.UserID = "directory-owner"
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	navPattern := regexp.MustCompile(`(?s)<nav class="apps-tabs"[^>]*>(.*?)</nav>`)
	hrefPattern := regexp.MustCompile(`href="([^"]*)"`)
	read := func(ref string, reversed bool) string {
		t.Helper()
		q := url.Values{"view": {"all"}, "search": {search}, "category": {category}}
		if ref != "" {
			q.Set("interface_revision", ref)
		}
		w := get("/settings/apps?" + q.Encode())
		if w.Code != 200 {
			t.Fatal("actual directory source unavailable", ref, w.Code, w.Body.String())
		}
		if pages {
			detailQuery := url.Values{"app": {"gmail"}}
			if ref != "" {
				detailQuery.Set("interface_revision", ref)
			}
			detail := get("/settings/apps?" + detailQuery.Encode())
			if detail.Code != 200 {
				t.Fatal("actual page source unavailable", ref, detail.Code, detail.Body.String())
			}
			header := regexp.MustCompile(`(?s)<header>.*?</header>`).FindString(detail.Body.String())
			title := strings.Index(header, `<h2>Gmail</h2>`)
			description := strings.Index(header, `<p>Read and organize your email</p>`)
			if title < 0 || description < 0 || (description < title) != reversed {
				t.Fatal("actual independent page source order missing", ref, reversed, header)
			}
		}
		nav := navPattern.FindStringSubmatch(w.Body.String())
		if len(nav) != 2 {
			t.Fatal("actual navigation missing")
		}
		var links []string
		for _, m := range hrefPattern.FindAllStringSubmatch(nav[1], -1) {
			links = append(links, html.UnescapeString(m[1]))
		}
		want := []string{"/settings/apps", "/settings/apps?view=connected", "/settings/apps?view=all"}
		if reversed && !pages {
			want[0], want[2] = want[2], want[0]
		}
		if !reflect.DeepEqual(links, want) {
			t.Fatal("managed source iteration lost original navigation", ref, links)
		}
		if !strings.Contains(w.Body.String(), `name="search" value="mail &amp; jobs"`) || !strings.Contains(w.Body.String(), `<option value="communication" selected>Communication</option>`) {
			t.Fatal("filter lost original query or category", ref)
		}
		for _, m := range hrefPattern.FindAllStringSubmatch(w.Body.String(), -1) {
			href := html.UnescapeString(m[1])
			u, e := url.Parse(href)
			if e == nil && u.Query().Get("cursor") == next {
				q := u.Query()
				if u.Path != "/settings/apps" || len(q) != 4 || q.Get("search") != search || q.Get("category") != category || q.Get("view") != "all" {
					t.Fatal("pagination reinterpreted original query", href)
				}
				return href
			}
		}
		t.Fatal("original continuation missing")
		return ""
	}
	old, oldPolicy := prepareInterface(t, ctx, host, oldVersion)
	activateInterface(t, ctx, host, "", old, oldPolicy, "directory-controls-old5")
	read("", false)
	first, firstPolicy := prepareInterface(t, ctx, host, firstVersion)
	activateInterface(t, ctx, host, old, first, firstPolicy, "directory-controls-first6")
	more := read("", false)
	read(old, false)
	if w := get(more); w.Code != 200 {
		t.Fatal("actual original continuation failed", w.Code)
	}
	mu.Lock()
	last := requests[len(requests)-1]
	mu.Unlock()
	if last.Get("cursor") != next || last.Get("search") != search || last.Get("category") != category {
		t.Fatal("actual API did not receive original continuation", last)
	}
	second, secondPolicy := prepareInterface(t, ctx, host, secondVersion)
	activateInterface(t, ctx, host, first, second, secondPolicy, "directory-controls-upgrade")
	read("", true)
	read(first, false)
	read(old, false)
	activateInterface(t, ctx, host, second, first, firstPolicy, "directory-controls-rollback")
	read("", false)
	read(second, true)
	activateInterface(t, ctx, host, first, second, secondPolicy, "directory-controls-before-cold")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	// First dependent reads after recovery; no reselect or fixture repair.
	read("", true)
	read(first, false)
	read(old, false)
	afterRead, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(before, afterRead) {
		t.Fatal("navigation reads changed authoritative settings/accounts", err)
	}
	bad, badPolicy := prepareInterface(t, ctx, host, badVersion)
	activateInterface(t, ctx, host, second, bad, badPolicy, "directory-controls-invalid")
	if w := get("/settings/apps?view=all"); w.Code != 503 {
		t.Fatal("invalid current navigation restored native", w.Code)
	}
	read(first, false)
	if _, err := host.Store.Deactivate(ctx, "directory-controls-stop", bad); err != nil {
		t.Fatal(err)
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
	read(old, false)
	read(first, false)
	read(second, true)
	if w := get("/settings/apps?view=all"); w.Code != 503 {
		t.Fatal("cold deactivation restored native directory", w.Code)
	}
	if _, _, _, _, _, actualBoundary, err := a.store.AppsInterfaceSelection(ctx); err != nil || actualBoundary != boundary {
		t.Fatal("directory source boundary lost", actualBoundary, err)
	}
	recovered, err := a.store.LoadApps(ctx)
	if err != nil || !reflect.DeepEqual(before, recovered) {
		t.Fatal("cold navigation changed original facts", err)
	}
}
