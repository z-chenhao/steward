package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const ownerToken = "synthetic-owner-directory-source-token-24"
	const sourceKey = "synthetic-project-key-host-only-keep-original"
	const sourceRef = "credential:directory-source"
	if err := secrets.Put(ctx, "credential:directory-owner", []byte(ownerToken)); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, sourceRef, []byte(sourceKey)); err != nil {
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

	orderRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-card-order-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var orderCases []struct {
		Name     string
		Items    []struct{ Slug, Name string }
		Expected []int
	}
	if err := json.Unmarshal(orderRaw, &orderCases); err != nil {
		t.Fatal(err)
	}
	orderFixture := orderCases[1]
	var mu sync.Mutex
	var requests []url.Values
	toolkitCalls := 0
	switchEntered, switchRelease := make(chan struct{}), make(chan struct{})
	lateEntered, lateRelease := make(chan struct{}), make(chan struct{})
	var switchOnce, lateOnce sync.Once
	releaseSwitch := func() { switchOnce.Do(func() { close(switchRelease) }) }
	defer releaseSwitch()
	releaseLate := func() { lateOnce.Do(func() { close(lateRelease) }) }
	defer releaseLate()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/toolkits/gmail" {
			mu.Lock()
			toolkitCalls++
			mu.Unlock()
			if r.Method != "GET" || r.Header.Get("x-api-key") != sourceKey {
				t.Error("original toolkit authority changed")
				http.Error(w, "invalid", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: "gmail", Name: "Provider Gmail", NoAuth: true})
			return
		}
		q := r.URL.Query()
		mu.Lock()
		requests = append(requests, q)
		mu.Unlock()
		if r.Method != "GET" || r.URL.Path != "/toolkits" || r.Header.Get("x-api-key") != sourceKey || q.Get("type") != "native" || q.Get("include_deprecated") != "false" || q.Get("sort_by") != "alphabetically" {
			t.Error("original source authority/scope changed", r.Method, r.URL)
			http.Error(w, "invalid source", 400)
			return
		}
		if q.Get("search") == "switch-categories" {
			close(switchEntered)
			<-switchRelease
		}
		if q.Get("search") == "quarantine-categories" {
			close(lateEntered)
			<-lateRelease
		}
		if q.Get("search") == "denied" {
			http.Error(w, "synthetic upstream failure", 500)
			return
		}
		slug, name, next := "remote_a", "Remote A", "opaque-original-"+q.Get("limit")+"+next/="
		if cursor := q.Get("cursor"); cursor != "" {
			if cursor != "opaque-original-40+next/=" && cursor != "opaque-original-20+next/=" {
				t.Error("opaque cursor changed", cursor)
				http.Error(w, "invalid cursor", 400)
				return
			}
			if q.Get("limit") != "" {
				t.Error("current page size overrode original cursor", q)
			}
			slug, name, next = "remote_b", "Remote B", ""
		}
		item := composio.Toolkit{Slug: slug, Name: name}
		item.Meta.Categories = []composio.Category{{ID: "dup", Name: "First Dup"}, {ID: "tail", Name: "Tail"}, {ID: "dup", Name: "Latest Dup"}}
		switch q.Get("search") {
		case "caption-version":
			item.Meta.Description = strings.Repeat("汉", 151)
		case "invalid-caption":
			item.Meta.Description = "1234567890123"
		case "invalid-categories":
			item.Meta.Categories = nil
			for i := 0; i < 5; i++ {
				item.Meta.Categories = append(item.Meta.Categories, composio.Category{ID: fmt.Sprint(i), Name: fmt.Sprint(i)})
			}
		case "large-categories":
			item.Meta.Categories = []composio.Category{{ID: "large", Name: strings.Repeat("<", 3<<20)}}
		case "many-categories":
			item.Meta.Categories = nil
			for i := 0; i < 70003; i++ {
				item.Meta.Categories = append(item.Meta.Categories, composio.Category{ID: fmt.Sprintf("category_%06d", i), Name: "Name"})
			}
		}

		if q.Get("search") == "invalid-order" {
			items := make([]composio.Toolkit, 7)
			for i := range items {
				items[i] = composio.Toolkit{Slug: fmt.Sprintf("guarded_%d", i), Name: "Guarded order row"}
			}
			_ = json.NewEncoder(w).Encode(composio.Catalog{Items: items})
			return
		}
		if q.Get("search") == "labels-order" {
			items := []composio.Toolkit{}
			for _, row := range orderFixture.Items {
				items = append(items, composio.Toolkit{Slug: row.Slug, Name: row.Name})
			}
			_ = json.NewEncoder(w).Encode(composio.Catalog{Items: items, Total: len(items)})
			return
		}
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(composio.Catalog{Items: []composio.Toolkit{item}, NextCursor: next, Total: 2})
	}))
	defer func() { releaseSwitch(); releaseLate(); peer.Close() }()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(requests) }
	toolkitCount := func() int { mu.Lock(); defer mu.Unlock(); return toolkitCalls }
	last := func() url.Values { mu.Lock(); defer mu.Unlock(); return requests[len(requests)-1] }
	host := a.Plugins()
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadEditorial = func(c context.Context, q contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			return a.readAppsEditorialWithHost(c, host, q)
		}
	}
	bind()
	server := &web.Server{Config: web.Config{TokenRef: "credential:directory-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	get := func(path string) (int, string) {
		t.Helper()
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		request.Header.Set("Authorization", "Bearer "+ownerToken)
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		return result.Code, result.Body.String()
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

	assertLabelOrder := func(beta bool) {
		t.Helper()
		code, body := get("/settings/apps?view=all&search=labels-order")
		if code != 200 {
			t.Fatal("actual label source failed", code, body)
		}
		expected := []string{}
		for _, index := range orderFixture.Expected {
			expected = append(expected, orderFixture.Items[index].Slug)
		}
		if beta {
			expected = []string{"z", "fallback", "b", "a"}
		}
		previous := -1
		for _, slug := range expected {
			at := strings.Index(body, `data-app-tile="`+slug+`"`)
			if at < 0 || at <= previous {
				t.Fatal("labels were projected after native sorting", expected, slug, at, previous)
			}
			previous = at
		}
		at := strings.Index(body, `data-app-tile="a"`)
		tile := body[at:]
		tile = tile[:strings.Index(tile, "</article>")]
		name, initial := "alpha", "A"
		if beta {
			name, initial = "a", "a"
		}
		if !strings.Contains(tile, "<strong>"+name+"</strong>") || !strings.Contains(tile, "<span>"+initial+"</span><img") {
			t.Fatal("actual name source or glyph case ignored", tile)
		}
	}
	assertCaptionHTTP := func(end int) {
		t.Helper()
		code, body := get("/settings/apps?view=all&search=caption-version")
		if code != 200 || !strings.Contains(body, strings.Repeat("汉", end)+"…") || strings.Contains(body, strings.Repeat("汉", end+1)) {
			t.Fatal("actual HTTP ignored selected caption method", code, body)
		}
	}
	first, _ := activateEditorialVersion(t, ctx, host, "", "1.0.0")
	before := count()
	offline, err := a.connectedApps.View(ctx, "gmail", "", "")
	if err != nil || !offline.Suggested || len(offline.Catalog.Items) != 1 || count() != before {
		t.Fatal("unconfigured source acquired network authority", offline, err)
	}
	state, err := a.connectedApps.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.connectedApps.ConfigureProject(ctx, sourceRef, state.Revision, nil); err != nil {
		t.Fatal(err)
	}
	if last().Get("limit") != "40" {
		t.Fatal("independent owner project probe lost original recipe", last())
	}
	assertLabelOrder(false)
	assertCaptionHTTP(147)
	query := "raw + 汉字"
	page, err := a.connectedApps.View(ctx, query, "productivity", "")
	if err != nil || page.Suggested || page.Unavailable || len(page.Catalog.Items) != 1 || page.Catalog.Items[0].Slug != "remote_a" || last().Get("limit") != "40" || last().Get("search") != query || last().Get("category") != "productivity" {
		t.Fatal("actual original live source/first page changed", page, err, last())
	}
	if len(page.Categories) != 3 || page.Categories[0].Name != "First Dup" || page.Categories[1].ID != "tail" || page.Categories[2].ID != "productivity" {
		t.Fatal("default category policy changed", page.Categories)
	}
	large, err := a.connectedApps.View(ctx, "large-categories", "", "")
	if err != nil || len(large.Categories) != 1 || len(large.Categories[0].Name) != 3<<20 {
		t.Fatal("large original label was truncated or placed in a small frame", err, len(large.Categories))
	}
	many, err := a.connectedApps.View(ctx, "many-categories", "", "")
	if err != nil || len(many.Categories) != 70003 || many.Categories[70002].ID != "category_070002" {
		t.Fatal("category input chunk/page completion lost original facts", err, len(many.Categories))
	}
	originalDetail, err := a.connectedApps.Detail(ctx, "gmail")
	if err != nil || originalDetail.Name != "Provider Gmail" || toolkitCount() != 1 {
		t.Fatal("original live-first detail was not dispatched", originalDetail, err)
	}
	// Explicit owner Connect creates a real durable public binding through the
	// original validation/registry path; its factual name never reaches a plugin.
	if _, err := a.connectedApps.Connect(ctx, "gmail", "owner", ""); err != nil {
		t.Fatal(err)
	}
	cursor := page.Catalog.NextCursor
	failed, err := a.connectedApps.View(ctx, "denied", "", "")
	if err != nil || !failed.Unavailable || failed.Suggested {
		t.Fatal("default failure policy changed", failed, err)
	}
	reopen()
	type viewResult struct {
		value connectedapps.View
		err   error
	}
	switched := make(chan viewResult, 1)
	go func() {
		value, err := a.connectedApps.View(ctx, "switch-categories", "productivity", "")
		switched <- viewResult{value, err}
	}()
	select {
	case <-switchEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("source switch boundary not reached")
	}
	second, _ := activateEditorialVersion(t, ctx, host, first, "1.1.0")
	releaseSwitch()
	select {
	case result := <-switched:
		if result.err != nil || len(result.value.Categories) != 3 || result.value.Categories[0].Name != "First Dup" {
			t.Fatal("one request mixed metadata and category policy versions", result.err, result.value.Categories)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("source switch did not drain")
	}

	next, err := a.connectedApps.View(ctx, query, "productivity", cursor)
	if err != nil || len(next.Catalog.Items) != 1 || next.Catalog.Items[0].Slug != "remote_b" || next.Catalog.NextCursor != "" || last().Get("cursor") != cursor || last().Get("limit") != "" || last().Get("search") != query || last().Get("category") != "productivity" {
		t.Fatal("upgrade lost/replayed the original page", next, err, last())
	}
	if len(next.Categories) != 3 || next.Categories[0].ID != "tail" || next.Categories[1].Name != "Latest Dup" {
		t.Fatal("independent last-occurrence policy was not dispatched", next.Categories)
	}
	fresh, err := a.connectedApps.View(ctx, query, "productivity", "")
	if err != nil || fresh.Catalog.NextCursor != "opaque-original-20+next/=" || last().Get("limit") != "20" {
		t.Fatal("independent page-size version was not dispatched", fresh, err, last())
	}
	lookups := toolkitCount()
	selectedDetail, err := a.connectedApps.Detail(ctx, "gmail")
	if err != nil || selectedDetail.Name != "Provider Gmail" || toolkitCount() != lookups {
		t.Fatal("independent connection-first/local detail policy was not used", selectedDetail, err)
	}
	detailStatus, detailBody := get("/settings/apps?app=gmail")
	if detailStatus != 200 || !strings.Contains(detailBody, "Provider Gmail") || toolkitCount() != lookups {
		t.Fatal("actual detail HTTP bypassed accepted local policy", detailStatus, detailBody)
	}
	before = count()
	local, err := a.connectedApps.View(ctx, "prefer-local", "", "")
	if err != nil || !local.Suggested || local.Unavailable || count() != before {
		t.Fatal("independent source preference still called provider", local, err)
	}
	fallback, err := a.connectedApps.View(ctx, "denied", "", "")
	if err != nil || !fallback.Suggested || fallback.Unavailable || len(fallback.Catalog.Items) != 0 {
		t.Fatal("independent failure policy did not use accepted editorial result", fallback, err)
	}
	assertLabelOrder(true)
	assertCaptionHTTP(117)
	status, body := get("/settings/apps?view=all&search=" + url.QueryEscape(query) + "&category=productivity")
	if status != 200 || !strings.Contains(body, "remote_a") || strings.Contains(body, sourceKey) || last().Get("limit") != "20" {
		t.Fatal("actual owner HTTP lost managed source recipe", status, body, last())
	}
	reopen()
	if _, err := a.connectedApps.View(ctx, query, "productivity", fresh.Catalog.NextCursor); err != nil || last().Get("limit") != "" {
		t.Fatal("cold continuation changed recipe", err, last())
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "source-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore original directory source policy"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "source-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "source-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	reopen()
	restored, err := a.connectedApps.View(ctx, query, "productivity", fresh.Catalog.NextCursor)
	if err != nil || len(restored.Catalog.Items) != 1 || restored.Catalog.Items[0].Slug != "remote_b" || last().Get("cursor") != fresh.Catalog.NextCursor || last().Get("limit") != "" {
		t.Fatal("rollback/cold restart lost the original20-item cursor", restored, err, last())
	}
	if _, err := a.connectedApps.View(ctx, query, "productivity", ""); err != nil || last().Get("limit") != "40" {
		t.Fatal("rollback did not restore first-page recipe", err, last())
	}
	assertLabelOrder(false)
	assertCaptionHTTP(147)
	third, _ := activateEditorialVersion(t, ctx, host, first, "2.0.0")
	before = count()
	badOrderStatus, badOrderBody := get("/settings/apps?view=all&search=invalid-order")
	if badOrderStatus == 200 || strings.Contains(badOrderBody, "Guarded order row") || count() != before+1 {
		t.Fatal("bad order released source rows or retried", badOrderStatus, count(), before)
	}

	before = count()
	invalidStatus, invalidBody := get("/settings/apps?view=all&search=invalid-caption")
	if invalidStatus == 200 || strings.Contains(invalidBody, "1234567890123") || count() != before+1 {
		t.Fatal("invalid caption released private source or retried native", invalidStatus, count(), before)
	}

	before = count()
	for _, query := range []string{"invalid-source", "invalid-page", "invalid-lookup", "invalid-labels"} {
		if _, err := a.connectedApps.View(ctx, query, "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || count() != before {
			t.Fatal("candidate schema weakened independent Host/HTTP guard", query, err, count(), before)
		}
	}
	before = count()
	invalid, err := a.connectedApps.View(ctx, "invalid-categories", "", "")
	if !errors.Is(err, connectedapps.ErrUnavailable) || len(invalid.Catalog.Items) != 0 || len(invalid.Categories) != 0 || count() != before+1 {
		t.Fatal("bad bitmap released source data or retried", err, len(invalid.Catalog.Items), count(), before)
	}
	late := make(chan viewResult, 1)
	go func() {
		value, err := a.connectedApps.View(ctx, "quarantine-categories", "", "")
		late <- viewResult{value, err}
	}()
	select {
	case <-lateEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("late category fence not reached")
	}
	if err := host.Store.Quarantine(ctx, third); err != nil {
		t.Fatal(err)
	}
	releaseLate()
	select {
	case result := <-late:
		if !errors.Is(result.err, connectedapps.ErrUnavailable) || len(result.value.Catalog.Items) != 0 || len(result.value.Categories) != 0 {
			t.Fatal("late quarantine released original source facts", result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("late category fence did not drain")
	}
	before = count()
	if _, err := a.connectedApps.View(ctx, "", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || count() != before {
		t.Fatal("quarantine restored native live catalogue", err)
	}
	if _, err := host.Store.Deactivate(ctx, "directory-source-disable", third); err != nil {
		t.Fatal(err)
	}
	reopen()
	if _, err := a.connectedApps.View(ctx, "", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || count() != before {
		t.Fatal("cold deactivation restored native live catalogue", err)
	}
	lookups = toolkitCount()
	fact, err := a.connectedApps.Detail(ctx, "gmail")
	if err != nil || fact.Name != "Provider Gmail" || toolkitCount() != lookups {
		t.Fatal("disabled directory hid real account facts or performed a lookup", fact, err)
	}
	if _, err := a.connectedApps.Detail(ctx, "unconnected"); !errors.Is(err, connectedapps.ErrUnavailable) || toolkitCount() != lookups {
		t.Fatal("disabled directory restored unrelated native detail lookup", err)
	}
	state, err = a.connectedApps.State(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].Toolkit != "gmail" || !state.Connections[0].Public || state.KeyRef != sourceRef {
		t.Fatal("source policy changed independent owner authority", state, err)
	}
	if err := a.connectedApps.ConfigureProject(ctx, sourceRef, state.Revision, nil); err != nil || count() != before+1 || last().Get("limit") != "40" {
		t.Fatal("directory disable broke independent owner project controls", err, last())
	}
	key, err := secrets.Get(ctx, sourceRef)
	if err != nil || string(key) != sourceKey {
		t.Fatal("source credential changed", err)
	}
}
