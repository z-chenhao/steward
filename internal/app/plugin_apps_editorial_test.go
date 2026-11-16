package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func editorialVersionPackage(t *testing.T, version string) string {
	t.Helper()
	if version == "1.0.0" {
		return defaultFeaturePackage(t, "apps-directory", nil)
	}
	directory := t.TempDir()
	path, err := filepath.Abs("../plugin/appsdirectory/editorial.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
	needle := "return rsiplugin.JSONResult(ctx, value)"
	if !strings.Contains(code, needle) {
		t.Fatal("editorial candidate source marker changed")
	}
	code = strings.Replace(code, "value.Discovery = discovery(value.Items)", `value.Items[0].Name="Mailbox beta";value.Items[0].Description="Versioned editorial beta";value.Discovery = discovery(value.Items)`, 1)
	code = strings.Replace(code, needle, `value.Labels=&contract.LabelPolicy{Named:contract.LabelPlan{NameSource:"slug"},Unnamed:contract.LabelPlan{NameSource:"slug"}};value.Lookup.PreferLive=false;value.Lookup.LocalFirst="connection";if input.LiveConfigured {if !input.HasCursor {value.Route.PageSize=20};value.Route.OnLiveError="editorial";if input.Search=="prefer-local" {value.Route=&contract.Route{Source:"editorial"}}};if input.Search=="uniquehint" {value.Filter.Slugs=[]string{"gmail"}};value.Discovery.Home[0].Slugs[0],value.Discovery.Home[0].Slugs[1]=value.Discovery.Home[0].Slugs[1],value.Discovery.Home[0].Slugs[0];`+needle, 1)

	if version == "1.2.0" {
		code = strings.Replace(code, `value.Labels=&contract.LabelPolicy{Named:contract.LabelPlan{NameSource:"slug"},Unnamed:contract.LabelPlan{NameSource:"slug"}};`, `value.Labels=labels();`, 1)
	}
	if version == "2.0.0" {
		code = strings.Replace(code, needle, `if input.Search=="invalid-labels" {value.Labels.Named.NameSource="account"};if input.Search=="invalid-source" {value.Route=&contract.Route{Source:"legacy"}};if input.Search=="invalid-page" {value.Route.PageSize=1001};if input.Search=="invalid-lookup" {value.Lookup.LocalFirst="arbitrary"};`+needle, 1)
	}
	override := filepath.Join(t.TempDir(), "editorial.go")
	if err := os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	categoryPath, err := filepath.Abs("../plugin/appsdirectory/categories.go")
	if err != nil {
		t.Fatal(err)
	}
	categoryRaw, err := os.ReadFile(categoryPath)
	if err != nil {
		t.Fatal(err)
	}
	categoryCode := string(categoryRaw)
	marker := "for i, key := range keys {"
	if !strings.Contains(categoryCode, marker) {
		t.Fatal("category policy marker changed")
	}
	categoryCode = strings.Replace(categoryCode, marker, "for i:=len(keys)-1;i>=0;i-- { key:=keys[i]", 1)
	if version == "2.0.0" {
		categoryCode = strings.Replace(categoryCode, "plan := chooseCategories(keys, request)", "plan := chooseCategories(keys, request);if request.Count==5 {plan.Keep=[]byte{255}}", 1)
	}
	categoryOverride := filepath.Join(t.TempDir(), "categories.go")
	if err := os.WriteFile(categoryOverride, []byte(categoryCode), 0600); err != nil {
		t.Fatal(err)
	}

	captionPath, err := filepath.Abs("../plugin/appsdirectory/captions.go")
	if err != nil {
		t.Fatal(err)
	}
	captionRaw, err := os.ReadFile(captionPath)
	if err != nil {
		t.Fatal(err)
	}
	captionCode := strings.Replace(string(captionRaw), "if p.End > 150 {", "if p.End > 120 {", 1)
	captionCode = strings.Replace(captionCode, "p.End = 147", "p.End = 117", 1)
	if version == "2.0.0" {
		captionCode = strings.Replace(captionCode, "p := contract.CaptionPlan{", `if f.ProviderRunes==13 {return contract.CaptionPlan{Source:"provider",End:14}};p := contract.CaptionPlan{`, 1)
	}
	captionOverride := filepath.Join(t.TempDir(), "captions.go")
	if err := os.WriteFile(captionOverride, []byte(captionCode), 0600); err != nil {
		t.Fatal(err)
	}

	orderPath, err := filepath.Abs("../plugin/appsdirectory/order.go")
	if err != nil {
		t.Fatal(err)
	}
	orderRaw, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatal(err)
	}
	orderCode := string(orderRaw)
	if !strings.Contains(orderCode, "sort.Slice(indices, less)") {
		t.Fatal("order policy marker changed")
	}
	orderCode = strings.Replace(orderCode, "sort.Slice(indices, less)", "sort.Slice(indices,func(i,j int)bool{return less(j,i)})", 1)
	if version == "2.0.0" {
		orderCode = strings.Replace(orderCode, "return rsiplugin.JSONResult(ctx, page)", `if request.Count==7 {page.Indices[1]=page.Indices[0]};if request.Count==8 {page.Indices[0]=request.Count};if request.Count==9 {page.Indices=page.Indices[:len(page.Indices)-1]};if request.Count==contract.OrderPageSize+3&&request.Offset>0 {page.Indices[0]=indices[0]};return rsiplugin.JSONResult(ctx, page)`, 1)
	}
	orderOverride := filepath.Join(t.TempDir(), "order.go")
	if err := os.WriteFile(orderOverride, []byte(orderCode), 0600); err != nil {
		t.Fatal(err)
	}

	statePath, err := filepath.Abs("../plugin/appsdirectory/card_states.go")
	if err != nil {
		t.Fatal(err)
	}
	stateRaw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stateCode := string(stateRaw)
	marker = "if fact&contract.FactActive != 0 {"
	if !strings.Contains(stateCode, marker) {
		t.Fatal("card state marker changed")
	}
	stateCode = strings.Replace(stateCode, marker, "if fact&contract.FactActive != 0 && plan.Active==nil {", 1)
	stateCode = strings.Replace(stateCode, "if plan.Managed == nil && (", "if (", 1)
	if version == "2.0.0" {
		stateCode = strings.Replace(stateCode, "return rsiplugin.JSONResult(ctx, plan)", `if request.Count==7 {value:=request.Count;plan.Active=&value};if request.Count==8 {value:=0;plan.Active=&value};if request.Count==9 {value:=0;plan.Pending=&value};return rsiplugin.JSONResult(ctx, plan)`, 1)
	}
	stateOverride := filepath.Join(t.TempDir(), "card_states.go")
	if err := os.WriteFile(stateOverride, []byte(stateCode), 0600); err != nil {
		t.Fatal(err)
	}
	privateOrderPath, err := filepath.Abs("../plugin/appsdirectory/inventory_order.go")
	if err != nil {
		t.Fatal(err)
	}
	privateOrderRaw, err := os.ReadFile(privateOrderPath)
	if err != nil {
		t.Fatal(err)
	}
	privateOrderCode := string(privateOrderRaw)
	if !strings.Contains(privateOrderCode, "sort.Slice(indices, less)") {
		t.Fatal("private order policy marker changed")
	}
	privateOrderCode = strings.Replace(privateOrderCode, "sort.Slice(indices, less)", "sort.Slice(indices,func(i,j int)bool{return less(j,i)})", 1)
	if version == "2.0.0" {
		privateOrderCode = strings.Replace(privateOrderCode, "return rsiplugin.JSONResult(ctx, page)", `if request.Count==7 {page.Indices[1]=page.Indices[0]};if request.Count==8 {page.Indices[0]=request.Count};if request.Count==9 {page.Indices=page.Indices[:len(page.Indices)-1]};if request.Count==contract.OrderPageSize+3&&request.Offset>0 {page.Indices[0]=indices[0]};return rsiplugin.JSONResult(ctx, page)`, 1)
	}
	privateOrderOverride := filepath.Join(t.TempDir(), "inventory_order.go")
	if err := os.WriteFile(privateOrderOverride, []byte(privateOrderCode), 0600); err != nil {
		t.Fatal(err)
	}
	if version == "1.3.0" {
		// Independent private-order version: preserve every other policy byte.
		originalEditorial := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "1.3.0"`, 1)
		for file, data := range map[string][]byte{override: []byte(originalEditorial), categoryOverride: categoryRaw, captionOverride: captionRaw, orderOverride: orderRaw, stateOverride: stateRaw} {
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override, categoryPath: categoryOverride, captionPath: captionOverride, orderPath: orderOverride, statePath: stateOverride, privateOrderPath: privateOrderOverride}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/apps-directory").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/apps-directory/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if version == "2.0.0" {
		// A candidate owns its advertised schema. Runtime domain/driver guards must
		// remain effective even when that schema is deliberately weakened.
		var schema map[string]any
		if err := json.Unmarshal(manifest.Provides[0].OutputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		labels := schema["properties"].(map[string]any)["labels"].(map[string]any)["properties"].(map[string]any)
		named := labels["named"].(map[string]any)["properties"].(map[string]any)["name_source"].(map[string]any)
		delete(named, "enum")
		route := schema["properties"].(map[string]any)["route"].(map[string]any)["properties"].(map[string]any)
		route["source"].(map[string]any)["enum"] = []string{"live", "editorial", "legacy"}
		route["page_size"].(map[string]any)["maximum"] = 1001
		schema["properties"].(map[string]any)["lookup"].(map[string]any)["properties"].(map[string]any)["local_first"].(map[string]any)["enum"] = []string{"editorial", "connection", "arbitrary"}
		manifest.Provides[0].OutputSchema, _ = json.Marshal(schema)
	}
	manifest.Version = version
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-editorial-owner-token-at-least24"
	if err := secrets.Put(ctx, "credential:editorial-test", []byte(token)); err != nil {
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
	host.Catalog.Launcher = carrier
	var editorialReads atomic.Int64
	bindEditorial := func() {
		a.connectedApps.ReadEditorial = func(c context.Context, query contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			editorialReads.Add(1)
			return a.readAppsEditorialWithHost(c, host, query)
		}
	}
	bindEditorial()
	server := &web.Server{Config: web.Config{TokenRef: "credential:editorial-test"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
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
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		bindEditorial()
		server.Core, server.Apps = a, a.connectedApps
	}
	get := func(path string) (int, string) {
		t.Helper()
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		before := editorialReads.Load()
		server.ServeHTTP(response, request)
		if editorialReads.Load()-before != 1 {
			t.Fatal("owner HTTP must share one editorial read across its consumers", path, editorialReads.Load()-before)
		}
		return response.Code, response.Body.String()
	}
	browser := func(version, name, description, first string) {
		t.Helper()
		if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
			return
		}
		evidence := os.Getenv("STEWARD_APP_DIRECTORY_BROWSER_EVIDENCE")
		if evidence == "" {
			evidence = t.TempDir()
		}
		served := httptest.NewServer(server)
		defer served.Close()
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "node", "../../tests/system/apps_directory_plugins_browser.mjs")
		fixtureRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-editorial-v1.json")
		if err != nil {
			t.Fatal(err)
		}
		var expected contract.Catalogue
		if err := json.Unmarshal(fixtureRaw, &expected); err != nil {
			t.Fatal(err)
		}
		collection := append([]string(nil), expected.Discovery.Collections[0].Slugs...)
		queryCount := 0
		if version == "managed-1.1.0" {
			queryCount = 1
			collection[2], collection[3] = collection[3], collection[2]
		}

		allRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-public-order-editorial-v1.json")
		if err != nil {
			t.Fatal(err)
		}
		var all []string
		if err := json.Unmarshal(allRaw, &all); err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(struct {
			URL, Token, Name, Description, First, Evidence string
			Collection, All                                []string
			QueryCount                                     int
		}{URL: served.URL, Token: token, Name: name, Description: description, First: first, Evidence: filepath.Join(evidence, version), Collection: collection, All: all, QueryCount: queryCount})
		command.Stdin = strings.NewReader(string(input))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("managed directory browser: %v\n%s", err, out)
		}
	}
	read := func(name, description string) {
		t.Helper()
		view, err := a.connectedApps.View(ctx, "gmail", "", "")
		if err != nil || len(view.Catalog.Items) != 1 || view.Catalog.Items[0].Name != name || view.Catalog.Items[0].Meta.Description != description {
			t.Fatal("offline Service did not use exact editorial selection", view, err)
		}
		for _, search := range []string{"uniquehint", "mailbox"} {
			query, err := a.connectedApps.View(ctx, search, "", "")
			expected := 0
			if name != "Gmail" {
				expected = 1
			}
			if err != nil || len(query.Catalog.Items) != expected {
				t.Fatal("offline filter did not use the independently versioned query policy", search, query, err)
			}
			status, body := get("/settings/apps?search=" + search)
			if status != 200 || strings.Count(body, `data-app-tile="gmail"`) != expected {
				t.Fatal("actual HTTP ignored the selected filter", search, status, body)
			}
		}

		selection, err := a.connectedApps.Directory(ctx, contract.Query{})
		if err != nil {
			t.Fatal(err)
		}

		privateName := strings.Repeat("汉", 1024*1024)
		label, err := selection.Label(privateName, "original_slug")
		expectedName, expectedInitial := privateName, "汉"
		if name != "Gmail" {
			expectedName, expectedInitial = "original_slug", "o"
		}
		if err != nil || label.Name != expectedName || label.Initial != expectedInitial {
			t.Fatal("versioned labels altered original references or hit a small frame", len(label.Name), label.Initial, err)
		}
		count := 147
		if name != "Gmail" {
			count = 117
		}
		sources := make([]connectedapps.CaptionSources, 129)
		largeCaption := strings.Repeat("汉", 1024*1024)
		for i := range sources {
			sources[i].Provider = largeCaption
		}
		captions, err := selection.ProjectCaptions(ctx, sources)
		if err != nil || len(captions) != 129 {
			t.Fatal("caption batch lifecycle failed", len(captions), err)
		}
		for _, caption := range captions {
			if caption != strings.Repeat("汉", count)+"…" {
				t.Fatal("caption version or whole original source changed", len(caption))
			}
		}
		for _, path := range []string{"/settings/apps?view=all", "/settings/apps?app=gmail", "/settings/apps?collection=communication", "/settings/apps"} {
			status, body := get(path)
			labelName := name
			if name != "Gmail" {
				labelName = "gmail"
			}
			if status != 200 || (!strings.Contains(body, "<strong>"+labelName+"</strong>") && !strings.Contains(body, "<h2>"+labelName+"</h2>")) || !strings.Contains(body, description) {
				t.Fatal("actual owner HTTP lost the selected editorial", path, status, body)
			}
			if path == "/settings/apps" {
				gmail, drive := strings.Index(body, `href="/settings/apps?app=gmail"`), strings.Index(body, `href="/settings/apps?app=googledrive"`)
				if gmail < 0 || drive < 0 || (gmail < drive) != (name == "Gmail") {
					t.Fatal("actual HTTP did not use the plugin's independent featured ordering", gmail, drive)
				}
			}
		}
	}
	read("Gmail", "Read and organize your email")
	selectVersion := func(baseline, version string) (string, string) {
		return activateEditorialVersion(t, ctx, host, baseline, version)
	}
	first, _ := selectVersion("", "1.0.0")
	read("Gmail", "Read and organize your email")
	frozenQuery := contract.Query{Search: "gmail"}
	frozen, err := a.connectedApps.Directory(ctx, frozenQuery)
	if err != nil {
		t.Fatal(err)
	}
	second, digest := selectVersion(first, "1.1.0")
	originalLabel, err := frozen.Label("private lowercase name", "original_slug")
	if err != nil || originalLabel.Name != "private lowercase name" || originalLabel.Initial != "P" {
		t.Fatal("frozen label plan followed active version", originalLabel, err)
	}

	read("Mailbox beta", "Versioned editorial beta")
	originalCaption, err := frozen.ProjectCaptions(ctx, []connectedapps.CaptionSources{{Provider: strings.Repeat("x", 151)}})
	if err != nil || len(originalCaption) != 1 || originalCaption[0] != strings.Repeat("x", 147)+"…" {
		t.Fatal("one accepted request mixed caption versions", originalCaption, err)
	}
	reopen()
	read("Mailbox beta", "Versioned editorial beta")
	browser("managed-1.1.0", "gmail", "Versioned editorial beta", "googledrive")
	if err := host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	reopen()
	if _, err := a.connectedApps.View(ctx, "gmail", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("cold stopped owner read restored legacy data", err)
	}
	if status, body := get("/settings/apps?view=connected"); status != 200 {
		t.Fatal("stopped editorial disabled independent account management", status, body)
	}
	if err := host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	read("Mailbox beta", "Versioned editorial beta")
	view, err := a.connectedApps.View(connectedapps.WithEditorial(ctx, frozenQuery, frozen, nil), "gmail", "", "")
	if err != nil || len(view.Catalog.Items) != 1 || view.Catalog.Items[0].Name != "Gmail" || view.Catalog.Items[0].Meta.Description != "Read and organize your email" {
		t.Fatal("one accepted request mixed two public editorial versions", view, err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: "editorial-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore the verified original public editorial"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "editorial-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "editorial-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	if err := host.Store.Retire(ctx, digest); err != nil {
		t.Fatal(err)
	}
	read("Gmail", "Read and organize your email")
	browser("rollback-1.0.0", "Gmail", "Read and organize your email", "gmail")
	if err := host.Store.Quarantine(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := a.connectedApps.View(ctx, "gmail", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("quarantine restored native suggestions", err)
	}
	if captions, err := frozen.ProjectCaptions(ctx, []connectedapps.CaptionSources{{Provider: "original"}}); err == nil || captions != nil {
		t.Fatal("quarantine restored native caption", captions, err)
	}
	if _, err := host.Store.Deactivate(ctx, "editorial-disable", first); err != nil {
		t.Fatal(err)
	}
	reopen()
	if _, err := a.connectedApps.View(ctx, "gmail", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("deactivation restored native suggestions", err)
	}
	if status, body := get("/settings/apps"); status == 200 && strings.Contains(body, "Read and organize your email") {
		t.Fatal("HTTP restored legacy editorial after deactivation")
	}
	if status, body := get("/settings/apps?view=connected"); status != 200 {
		t.Fatal("editorial removal disabled independent account management", status, body)
	}
	state, err := a.connectedApps.State(ctx)
	if err != nil || len(state.Connections) != 0 || state.KeyRef != "" {
		t.Fatal("public editorial plugin acquired an account or key", state, err)
	}
}

func activateEditorialVersion(t *testing.T, ctx context.Context, host rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	installed, err := host.Install(ctx, editorialVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-directory", PackageDigest: installed.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-directory", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-editorial-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var original contract.Catalogue
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if version != "1.0.0" && version != "1.3.0" {
		original.Lookup.LocalFirst = "connection"
		original.Labels = &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "slug"}, Unnamed: contract.LabelPlan{NameSource: "slug"}}
		if version == "1.2.0" {
			original.Labels = &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "name", UpperInitial: true}, Unnamed: contract.LabelPlan{NameSource: "slug", UpperInitial: true}}
		}
		original.Items[0].Name = "Mailbox beta"
		original.Items[0].Description = "Versioned editorial beta"
		// Literal expectations derive from the captured original native output,
		// independently of the candidate's grouping/sorting implementation.
		original.Discovery.Home[0].Slugs[0], original.Discovery.Home[0].Slugs[1] = "googledrive", "gmail"
		original.Discovery.Collections[0].Slugs[2], original.Discovery.Collections[0].Slugs[3] = "googlecalendar", "gmail"
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-editorial-queries-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []struct {
		Query  contract.Query  `json:"query"`
		Filter contract.Filter `json:"filter"`
	}
	if err := json.Unmarshal(raw, &queries); err != nil {
		t.Fatal(err)
	}
	policy := rsi.Policy{ID: "editorial-owner-" + version, AllowedContracts: []string{contract.Editorial, contract.CategoryOptions, contract.Captions, contract.CatalogueOrder, contract.CardStates, contract.InventoryOrder}, AllowedRequests: []string{"input.read"}}
	for _, query := range queries {
		expected := original
		expected.Filter = &query.Filter
		if version != "1.0.0" && version != "1.3.0" && (query.Query.Search == "mailbox" || query.Query.Search == "uniquehint") {
			expected.Filter.Slugs = []string{"gmail"}
		}
		out, _ := json.Marshal(expected)
		input, _ := json.Marshal(query.Query)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.Editorial, Input: input, Expected: out})
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-directory-route-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes []struct {
		Query contract.Query `json:"query"`
		Route contract.Route `json:"route"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		expected := original
		expected.Route = &route.Route
		expected.Lookup = &contract.Lookup{PreferLive: route.Query.LiveConfigured && (version == "1.0.0" || version == "1.3.0"), LocalFirst: original.Lookup.LocalFirst, OnLiveError: "local"}
		if route.Query.Search == "prefer-local" {
			expected.Filter = &contract.Filter{Slugs: []string{}, Categories: original.Filter.Categories}
		}
		if version != "1.0.0" && version != "1.3.0" && route.Query.LiveConfigured {
			if !route.Query.HasCursor {
				expected.Route.PageSize = 20
			}
			expected.Route.OnLiveError = "editorial"
			if route.Query.Search == "prefer-local" {
				expected.Route = &contract.Route{Source: "editorial"}
			}
		}
		out, _ := json.Marshal(expected)
		input, _ := json.Marshal(route.Query)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.Editorial, Input: input, Expected: out})
	}
	for _, request := range []contract.CategoryRequest{{Count: 3}, {Count: 3, HasRequested: true, RequestedKey: 1}, {Count: 3, HasRequested: true}, {}, {HasRequested: true}} {
		data := []byte{}
		if request.Count == 3 {
			for _, key := range []uint32{1, 2, 1} {
				data = binary.LittleEndian.AppendUint32(data, key)
			}
		}
		keep := []byte{}
		if request.Count == 3 {
			if version == "1.0.0" || version == "1.3.0" {
				keep = []byte{3}
			} else {
				keep = []byte{6}
			}
		}
		expected := contract.CategoryPlan{Keep: keep, AppendRequested: request.HasRequested && request.RequestedKey == 0}
		input, _ := json.Marshal(request)
		out, _ := json.Marshal(expected)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.CategoryOptions, Input: input, Inputs: []rsi.InputData{{ID: contract.CategoryInputID, Data: data}}, Expected: out})
	}

	// Independent literal owner expectations; never call the candidate algorithm.
	captionEnd := 147
	if version != "1.0.0" && version != "1.3.0" {
		captionEnd = 117
	}
	captionInput, _ := json.Marshal([]contract.CaptionFacts{{ProviderRunes: 151}, {EditorialRunes: 2, ProviderRunes: 200}, {}})
	captionExpected, _ := json.Marshal([]contract.CaptionPlan{{Source: "provider", End: captionEnd, Text: "…"}, {Source: "editorial", End: 2}, {Source: "literal", Text: "Bring this app into your everyday work"}})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.Captions, Input: captionInput, Expected: captionExpected})

	orderRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-card-order-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var orderCases []struct {
		Name     string
		Items    []contract.OrderFact
		Expected []int
	}
	if err := json.Unmarshal(orderRaw, &orderCases); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range orderCases {
		expected := append([]int{}, fixture.Expected...)
		if version != "1.0.0" && version != "1.3.0" {
			switch fixture.Name {
			case "empty-name-and-case-tie":
				expected = []int{0, 1, 2, 3}
			case "unicode-and-space":
				expected = []int{2, 3, 0, 1, 4}
			}
		}

		if version == "1.2.0" {
			switch fixture.Name {
			case "empty-name-and-case-tie":
				expected = []int{1, 0, 2, 3}
			case "unicode-and-space":
				expected = []int{1, 0, 3, 4, 2}
			}
		}
		data, err := contract.EncodeOrderFacts(fixture.Items)
		if err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(contract.OrderRequest{Count: len(fixture.Items), Labels: *original.Labels})
		output, _ := json.Marshal(contract.OrderPage{Indices: expected})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.CatalogueOrder, Input: input, Inputs: []rsi.InputData{{ID: contract.OrderInputID, Data: data}}, Expected: output})
	}

	ptr := func(value int) *int { return &value }
	for _, fixture := range []struct {
		request  contract.CardStateRequest
		data     []byte
		expected contract.CardStatePlan
	}{
		{request: contract.CardStateRequest{}, data: []byte{}},
		{request: contract.CardStateRequest{Count: 1}, data: []byte{0}, expected: contract.CardStatePlan{Managed: ptr(0)}},
		{request: contract.CardStateRequest{Count: 1}, data: []byte{contract.FactDisconnected | contract.FactRevocationConfirmed}},
		{request: contract.CardStateRequest{Count: 3}, data: []byte{contract.FactDisconnected | contract.FactRevocationConfirmed, contract.FactPending, contract.FactActive | contract.FactPublic}, expected: contract.CardStatePlan{Managed: ptr(1), Active: ptr(2), Pending: ptr(1)}},
		{request: contract.CardStateRequest{Count: 2}, data: []byte{contract.FactActive | contract.FactPublic, contract.FactActive}, expected: contract.CardStatePlan{Managed: ptr(0), Active: ptr(1)}},
		{request: contract.CardStateRequest{Count: 3, Offset: 2, Prior: contract.CardStatePlan{Managed: ptr(0), Active: ptr(0)}}, data: []byte{contract.FactActive}, expected: contract.CardStatePlan{Managed: ptr(0), Active: ptr(2)}},
	} {
		if version != "1.0.0" && version != "1.3.0" && (fixture.request.Count == 2 || fixture.request.Offset == 2) {
			fixture.expected.Active = ptr(0)
		}
		if version != "1.0.0" && version != "1.3.0" {
			if fixture.request.Count == 3 {
				fixture.expected.Managed = ptr(2)
			}
			if fixture.request.Count == 2 {
				fixture.expected.Managed = ptr(1)
			}
		}
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(fixture.expected)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.CardStates, Input: input, Inputs: []rsi.InputData{{ID: contract.CardStateInputID, Data: fixture.data}}, Expected: output})
	}
	for _, connected := range []bool{false, true} {
		for _, fixture := range []struct {
			facts             []contract.InventoryOrderFact
			expected, reverse []int
		}{
			{facts: []contract.InventoryOrderFact{}, expected: []int{}, reverse: []int{}},
			{facts: []contract.InventoryOrderFact{{Name: "beta", Slug: "b"}, {Name: "Alpha", Slug: "z"}, {Name: "alpha", Slug: "a"}}, expected: []int{1, 2, 0}, reverse: []int{0, 1, 2}},
			{facts: []contract.InventoryOrderFact{{Name: "Ω"}, {Name: "é"}, {Name: "e\u0301"}, {Name: ""}}, expected: []int{3, 2, 1, 0}, reverse: []int{0, 1, 2, 3}},
		} {
			if connected && len(fixture.facts) == 3 {
				fixture.expected = []int{2, 1, 0}
			}
			if version != "1.0.0" {
				fixture.expected = fixture.reverse
			}
			input, _ := json.Marshal(contract.InventoryOrderRequest{Count: len(fixture.facts), Connected: connected})
			output, _ := json.Marshal(contract.OrderPage{Indices: fixture.expected})
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-directory", Contract: contract.InventoryOrder, Input: input, Inputs: []rsi.InputData{{ID: contract.InventoryOrderInputID, Data: contract.EncodeInventoryOrderFacts(fixture.facts)}}, Expected: output})
		}
	}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	id := "editorial-" + version
	if err := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: graph.Revision, PolicyID: policy.ID, Goal: "Independently update public editorial data without account authority"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, installed.Digest
}
