package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func interfacePackage(t *testing.T, version string) string {
	if version == "1.19.0" || version == "1.20.0" || version == "4.11.0" {
		return pagesInterfacePackage(t, version)
	}
	if version == "1.17.0" || version == "1.18.0" || version == "4.9.0" {
		return directoryControlsInterfacePackage(t, version)
	}
	if version == "1.15.0" || version == "1.16.0" || version == "4.7.0" {
		return settingsViewInterfacePackage(t, version)
	}
	t.Helper()
	if version == "1.13.0" || version == "1.14.0" || version == "4.5.0" {
		return connectViewInterfacePackage(t, version)
	}
	if version == "1.11.0" || version == "1.12.0" {
		return accountCopyInterfacePackage(t, version)
	}
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	profilePath, err := filepath.Abs("../plugin/appsinterface/profile.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(read("../../tests/fixtures/plugins/apps-interface-pre-account-v6/profile.go.txt"))
	if version == "1.9.0" || version == "1.10.0" || version == "4.3.0" {
		code = strings.Replace(string(read("../../tests/fixtures/plugins/apps-interface-pre-account-copy-v7/profile.go.txt")), `Version: "1.9.0"`, `Version: "1.0.0"`, 1)
	}
	oldMarkup := version == "0.4.0" || version == "3.2.0" || version == "3.3.0" || version == "3.5.0"
	legacy := oldMarkup || version == "0.9.0" || version == "0.8.0" || version == "0.7.0" || version == "0.6.0" || version == "0.5.0"
	if oldMarkup {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/profile.go.txt"))
	}
	if version == "0.5.0" {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-markup-v4/profile.go.txt"))
	}
	if version == "0.6.0" {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-sidebar-v3/profile.go.txt"))
	}
	if version == "0.7.0" {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-card-v2/profile.go.txt"))
	}
	if version == "0.9.0" {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-composition-v1/profile.go.txt"))
	}
	if version == "0.8.0" {
		code = string(read("../../tests/fixtures/plugins/apps-interface-pre-localization-v1/profile.go.txt"))
	}
	code = strings.Replace(code, `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
	mustReplace := func(text, old, new string) string {
		t.Helper()
		if !strings.Contains(text, old) {
			t.Fatal("actual interface variation marker missing", old)
		}
		return strings.Replace(text, old, new, 1)
	}
	if version == "1.6.0" {
		code = mustReplace(code, "p.Accounts[i] = account(i)", `p.Accounts[i] = account(i);if i==32{p.Accounts[i].StatusLabel="Workspace"}`)
	}
	if version == "1.1.0" {
		marker := "p.Accounts[i] = account(i)"
		code = mustReplace(code, marker, marker+`;a:=p.Accounts[i].Actions;for l,r:=0,len(a)-1;l<r;l,r=l+1,r-1 {a[l],a[r]=a[r],a[l]}`)
		code = mustReplace(code, "PollIntervalMS: 5000", "PollIntervalMS: 10000")
	}
	if version == "1.3.0" {
		code = mustReplace(code, `"Finish signing in in the authorization tab. This page checks automatically."`, `"Finish linking in the authorization tab."`)
		code = mustReplace(code, `"Account connected."`, `"Account ready."`)
		code = mustReplace(code, `AccountSaved: "App settings updated."`, `AccountSaved: "Ready"`)
		code = mustReplace(code, `"Check the app, account alias and credential reference."`, `"Input is invalid."`)
	}
	if version == "4.2.0" {
		code = mustReplace(code, "contract.GraphicMarkupInterface: compileGraphicMarkup", "contract.MarkupInterface: compileMarkup, contract.GraphicMarkupInterface: compileGraphicMarkup")
	}
	if version == "3.4.0" {
		code = mustReplace(code, "contract.LocalizedInterface: compileLocalizedProfile, contract.LocalizedComposition: compileLocalizedComposition, contract.CardViewInterface: compileCardView, contract.SidebarViewInterface: compileSidebarView, ", "")
	}
	if version == "2.8.0" || version == "2.9.0" {
		code = mustReplace(code, ", contract.GraphicMarkupInterface: compileGraphicMarkup", "")
	}
	if version == "2.9.0" {
		code = mustReplace(code, "contract.LocalizedInterface: compileLocalizedProfile, contract.LocalizedComposition: compileLocalizedComposition, contract.CardViewInterface: compileCardView, ", "")
	}
	if version == "2.8.0" {
		code = mustReplace(code, ", contract.SidebarViewInterface: compileSidebarView", "")
		code = mustReplace(code, "contract.LocalizedInterface: compileLocalizedProfile, contract.LocalizedComposition: compileLocalizedComposition, ", "")
	}
	if version == "2.4.0" {
		code = mustReplace(code, "contract.LocalizedInterface: compileLocalizedProfile, ", "")
	}
	if version == "2.2.0" || version == "2.3.0" {
		code = mustReplace(code, "contract.LocalizedInterface: compileLocalizedProfile,", "contract.Interface: compileLegacyProfileForTest, contract.CompositionInterface: compileLegacyCompositionForTest, contract.LocalizedInterface: compileLocalizedProfile,")
	}
	replacements := map[string]string{}
	replaceFile := func(path, content string) {
		t.Helper()
		replacement := filepath.Join(t.TempDir(), filepath.Base(path))
		if err := os.WriteFile(replacement, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = replacement
	}
	replaceFile(profilePath, code)
	localizationPath, _ := filepath.Abs("../plugin/appsinterface/localization.go")
	replaceFile(localizationPath, string(read("../../tests/fixtures/plugins/apps-interface-pre-account-copy-v7/localization.go.txt")))
	accountCopyPath, _ := filepath.Abs("../plugin/appsinterface/account_copy.go")
	replaceFile(accountCopyPath, "package appsinterface\n")
	markupPath, _ := filepath.Abs("../plugin/appsinterface/markup.go")
	if version != "1.9.0" && version != "1.10.0" && version != "4.3.0" {
		replaceFile(markupPath, string(read("../../tests/fixtures/plugins/apps-interface-pre-account-v6/markup.go.txt")))
	}
	if version == "4.2.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/markup.go")
		code := string(read("../../tests/fixtures/plugins/apps-interface-pre-account-v6/markup.go.txt"))
		old := string(read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/markup.go.txt"))
		i := strings.Index(old, "func compileMarkup(")
		if i < 0 {
			t.Fatal("old compiler absent")
		}
		replaceFile(path, code+"\n"+old[i:])
	}
	if oldMarkup {
		path, _ := filepath.Abs("../plugin/appsinterface/markup.go")
		replaceFile(path, string(read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/markup.go.txt")))
	}
	if version == "3.5.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/markup.go")
		code := string(read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/markup.go.txt"))
		code = mustReplace(code, "return rsiplugin.JSONResult(ctx, contract.Markup", `if call.OperationID=="owner-read"{raw=append(raw,' ')}`+"\n return rsiplugin.JSONResult(ctx, contract.Markup")
		replaceFile(path, code)
	}
	if version == "0.5.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/view_source.go")
		replaceFile(path, string(read("../../tests/fixtures/plugins/apps-interface-pre-markup-v4/view_source.go.txt")))
	}
	if version == "0.6.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/card.go")
		replaceFile(path, string(read("../../tests/fixtures/plugins/apps-interface-pre-sidebar-v3/card.go.txt")))
		path, _ = filepath.Abs("../plugin/appsinterface/contract/card.go")
		replaceFile(path, string(read("../../tests/fixtures/plugins/apps-interface-pre-sidebar-v3/card-contract.go.txt")))
	}
	if version == "3.0.0" || version == "3.1.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/sidebar.go")
		sidebar := string(read(path))
		mutation := `if call.OperationID=="owner-read" {source.Program[1]="name"}`
		if version == "3.1.0" {
			mutation = `if call.OperationID=="owner-read" {raw=append(raw,' ')}`
		}
		sidebar = mustReplace(sidebar, "return rsiplugin.JSONResult(ctx, contract.SidebarView", mutation+"\n return rsiplugin.JSONResult(ctx, contract.SidebarView")
		replaceFile(path, sidebar)
	}
	if version == "2.5.0" || version == "2.6.0" {
		path, _ := filepath.Abs("../plugin/appsinterface/card.go")
		card := string(read(path))
		mutation := `if call.OperationID=="owner-read" {source.Program[2]="manage"}`
		if version == "2.6.0" {
			mutation = `if call.OperationID=="owner-read" {raw=append(raw,' ')}`
		}
		card = mustReplace(card, "return rsiplugin.JSONResult(ctx, contract.CardView", mutation+"\n return rsiplugin.JSONResult(ctx, contract.CardView")
		replaceFile(path, card)
	}
	if version == "0.8.0" || version == "1.2.0" || version == "1.3.0" {
		path, err := filepath.Abs("../plugin/appsinterface/composition.go")
		if err != nil {
			t.Fatal(err)
		}
		code := string(read(path))
		if version == "0.8.0" {
			code = string(read("../../tests/fixtures/plugins/apps-interface-pre-localization-v1/composition.go.txt"))
		}
		if version == "1.2.0" {
			code = mustReplace(code, `[]string{"heading", "back", "project"}`, `[]string{"heading", "project", "back"}`)
			code = mustReplace(code, `"App connection service"`, `"Connection service"`)
		}
		if version == "1.3.0" {
			code = mustReplace(code, `"App connection service"`, `"Connection workspace"`)
		}
		replaceFile(path, code)
	}
	if strings.HasPrefix(version, "2.") || version == "1.3.0" {
		path, err := filepath.Abs("../plugin/appsinterface/localization.go")
		if err != nil {
			t.Fatal(err)
		}
		code := string(read("../../tests/fixtures/plugins/apps-interface-pre-account-copy-v7/localization.go.txt"))
		switch version {
		case "2.0.0":
			code = mustReplace(code, "p := profile()", `p := profile();if call.OperationID=="owner-read" {p.Accounts[2].Visible=true}`)
		case "2.1.0":
			code = mustReplace(code, "c := composition()", `c := composition();if call.OperationID=="owner-read" {c.Account[1]="copy"}`)
		case "2.2.0":
			code = mustReplace(code, "p := profile()", `p := profile();if call.OperationID=="owner-read" {p.Errors[0]=""}`)
		case "2.3.0":
			code = mustReplace(code, "c := composition()", `c := composition();if call.OperationID=="owner-read" {c.Copy.ServiceTitle=""}`)
		case "1.3.0":
			code = mustReplace(code, "var chineseText = map[string]string{", "var chineseText = map[string]string{\n"+`"Connection workspace":"Workspace","Finish linking in the authorization tab.":"请在授权标签页完成连接。","Account ready.":"Workspace","Ready":"Workspace","Input is invalid.":"Workspace",`)
		}
		if version == "2.2.0" || version == "2.3.0" {
			code += `
func compileLegacyProfileForTest(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){return rsiplugin.JSONResult(ctx,profile())}
func compileLegacyCompositionForTest(ctx context.Context,call rsiplugin.Call)(rsiplugin.Result,error){return rsiplugin.JSONResult(ctx,composition())}
`
		}
		replaceFile(path, code)
	}
	dir := t.TempDir()
	encoded, _ := json.Marshal(map[string]any{"Replace": replacements})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-interface").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw := read("../../tests/fixtures/plugins/apps-interface-pre-account-v6/manifest.json")
	if version == "1.9.0" || version == "1.10.0" || version == "4.3.0" {
		raw = read("../../tests/fixtures/plugins/apps-interface-pre-account-copy-v7/manifest.json")
	}
	if legacy {
		fixture := "apps-interface-pre-composition-v1"
		if oldMarkup {
			fixture = "apps-interface-pre-graphics-v5"
		}
		if version == "0.8.0" {
			fixture = "apps-interface-pre-localization-v1"
		}
		if version == "0.5.0" {
			fixture = "apps-interface-pre-markup-v4"
		}
		if version == "0.6.0" {
			fixture = "apps-interface-pre-sidebar-v3"
		}
		if version == "0.7.0" {
			fixture = "apps-interface-pre-card-v2"
		}
		raw = read("../../tests/fixtures/plugins/" + fixture + "/manifest.json")
	}
	if version == "4.2.0" {
		raw = read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/manifest.json")
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if version == "4.2.0" {
		var current rsiplugin.Manifest
		if err := rsiplugin.Decode(read("../../tests/fixtures/plugins/apps-interface-pre-account-v6/manifest.json"), &current); err != nil {
			t.Fatal(err)
		}
		for _, service := range current.Provides {
			if service.Contract == contract.GraphicMarkupInterface {
				manifest.Provides = append(manifest.Provides, service)
			}
		}
	}
	if version == "2.2.0" || version == "2.3.0" {
		var old rsiplugin.Manifest
		if err := rsiplugin.Decode(read("../../tests/fixtures/plugins/apps-interface-pre-localization-v1/manifest.json"), &old); err != nil {
			t.Fatal(err)
		}
		manifest.Provides = append(manifest.Provides, old.Provides...)
	}
	if version == "2.4.0" {
		filtered := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.LocalizedInterface {
				filtered = append(filtered, service)
			}
		}
		manifest.Provides = filtered
	}
	if version == "2.8.0" {
		filtered := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract == contract.CardViewInterface {
				filtered = append(filtered, service)
			}
		}
		manifest.Provides = filtered
	}
	if version == "2.9.0" {
		resources := manifest.Resources[:0]
		for _, resource := range manifest.Resources {
			if resource.Name == contract.SidebarViewResource {
				resources = append(resources, resource)
			}
		}
		manifest.Resources = resources
		filtered := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract == contract.SidebarViewInterface {
				filtered = append(filtered, service)
			}
		}
		manifest.Provides = filtered
	}
	if version == "2.8.0" {
		resources := manifest.Resources[:0]
		for _, resource := range manifest.Resources {
			if resource.Name != contract.SidebarViewResource {
				resources = append(resources, resource)
			}
		}
		manifest.Resources = resources
	}
	if version == "2.8.0" || version == "2.9.0" {
		resources := manifest.Resources[:0]
		for _, r := range manifest.Resources {
			if r.Name != contract.MarkupResource {
				resources = append(resources, r)
			}
		}
		manifest.Resources = resources
	}
	if version == "3.4.0" {
		services := manifest.Provides[:0]
		for _, v := range manifest.Provides {
			if v.Contract == contract.GraphicMarkupInterface {
				services = append(services, v)
			}
		}
		manifest.Provides = services
		resources := manifest.Resources[:0]
		for _, v := range manifest.Resources {
			if v.Name == contract.MarkupResource {
				resources = append(resources, v)
			}
		}
		manifest.Resources = resources
	}
	copyInterfaceViews(t, dir, manifest)
	for _, resource := range manifest.Resources {
		if resource.Name == contract.MarkupResource {
			fixture := "../../tests/fixtures/plugins/apps-interface-pre-account-v6/markup.json"
			if version == "1.9.0" || version == "1.10.0" || version == "4.3.0" {
				fixture = "../../tests/fixtures/plugins/apps-account-source-native-v3.json"
			}
			if err := os.WriteFile(filepath.Join(dir, contract.MarkupFile), read(fixture), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if version == "4.2.0" {
		if err := os.WriteFile(filepath.Join(dir, contract.MarkupFile), read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/markup.json"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if oldMarkup {
		if err := os.WriteFile(filepath.Join(dir, contract.MarkupFile), read("../../tests/fixtures/plugins/apps-interface-pre-graphics-v5/markup.json"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if version == "3.2.0" || version == "3.3.0" {
		resource := filepath.Join(dir, contract.MarkupFile)
		var source contract.MarkupSource
		if err := json.Unmarshal(read(resource), &source); err != nil {
			t.Fatal(err)
		}
		if version == "3.2.0" {
			source.Card = mustReplace(source.Card, `{{.Name}}`, `{{.Apps.State.KeyRef}}`)
		} else {
			source.Card = mustReplace(source.Card, `name="action" value="connect"`, `name="action" value="disconnect"`)
		}
		raw, _ := json.MarshalIndent(source, "", "  ")
		raw = append(raw, '\n')
		if err := os.WriteFile(resource, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if version == "1.6.0" {
		resource := filepath.Join(dir, contract.MarkupFile)
		var source contract.GraphicMarkupSource
		if err := json.Unmarshal(read(resource), &source); err != nil {
			t.Fatal(err)
		}
		source.Card = mustReplace(source.Card, `<span class="app-copy">`, `<div class="app-copy" data-plugin-copy="v1.6">`)
		source.Card = mustReplace(source.Card, `"app-card-atom-end-copy"}}</span>`, `"app-card-atom-end-copy"}}</div>`)
		source.Sidebar = mustReplace(source.Sidebar, `<span>{{.Name}}</span>`, `<span data-plugin-sidebar="v1.6">{{.Name}}</span>`)
		raw, _ := json.MarshalIndent(source, "", "  ")
		raw = append(raw, '\n')
		if err := os.WriteFile(resource, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}

	if version == "1.8.0" || version == "4.0.0" {
		resource := filepath.Join(dir, contract.MarkupFile)
		var source contract.GraphicMarkupSource
		if err := json.Unmarshal(read(resource), &source); err != nil {
			t.Fatal(err)
		}
		if version == "1.8.0" {
			source.Graphics = graphicVariantSource(source.Graphics)
		} else {
			source.Graphics = mustReplace(source.Graphics, "<svg ", `<svg onload="alert(1)" `)
		}
		raw, _ := json.MarshalIndent(source, "", "  ")
		raw = append(raw, '\n')
		if err := os.WriteFile(resource, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if version == "1.5.0" {
		resource := filepath.Join(dir, contract.SidebarViewFile)
		raw := string(read(resource))
		raw = mustReplace(raw, `    "icon",
    "name",`, `    "name",
    "icon",`)
		if err := os.WriteFile(resource, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if version == "1.4.0" {
		resource := filepath.Join(dir, contract.CardViewFile)
		raw := string(read(resource))
		raw = mustReplace(raw, `    "icon",
    "begin-copy",`, `    "begin-copy",`)
		raw = mustReplace(raw, `    "end-copy",`, `    "end-copy",
    "icon",`)
		if err := os.WriteFile(resource, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if version == "1.10.0" || version == "4.3.0" {
		resource := filepath.Join(dir, contract.MarkupFile)
		var source contract.AccountMarkupSource
		if err := json.Unmarshal(read(resource), &source); err != nil {
			t.Fatal(err)
		}
		if version == "1.10.0" {
			source.Account = mustReplace(source.Account, "{{.Name}} · {{.Alias}}", "{{.Alias}} · {{.Name}}")
		} else {
			source.Account = mustReplace(source.Account, "{{.Buttons}}", `{{.Buttons}}<button name="action" value="disconnect">Unexpected</button>`)
		}
		raw, _ := json.MarshalIndent(source, "", "  ")
		raw = append(raw, '\n')
		if err := os.WriteFile(resource, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Version = version
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func prepareInterface(t *testing.T, ctx context.Context, host rsi.Host, version string, accounts ...string) (string, string) {
	t.Helper()
	if version == "1.21.0" || version == "1.23.0" {
		return prepareBrowserInterface(t, ctx, host, version, accounts...)
	}
	pkg, err := host.Install(ctx, interfacePackage(t, version))
	if err != nil {
		t.Fatal("interface fixture prepare/install/policy", version, err)
	}
	bindings := []rsiplugin.Binding{{InstanceID: "apps-interface", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-interface", StateVersion: 1}}
	for _, revision := range accounts {
		previous, err := host.Store.Graph(ctx, revision)
		if err != nil {
			t.Fatal("interface fixture prepare/install/policy", version, err)
		}
		bindings = append(bindings, previous.Graph.Bindings...)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		t.Fatal("interface fixture prepare/install/policy", version, err)
	}
	policy := rsi.Policy{ID: "interface-" + version}
	if version != "0.5.0" && version != "0.6.0" && version != "0.7.0" && version != "0.8.0" && version != "0.9.0" && version != "2.8.0" && version != "2.9.0" {
		markupContract := contract.GraphicMarkupInterface
		markupFixture := "apps-graphics-source-native-v2.json"
		if version == "1.9.0" || version == "1.10.0" || version == "4.3.0" || version == "1.11.0" || version == "1.12.0" {
			markupContract = contract.AccountMarkupInterface
			markupFixture = "apps-account-source-native-v3.json"
		}
		if version == "1.11.0" || version == "1.12.0" {
			markupFixture = "apps-account-source-native-v3-original-spacing.json"
		}
		if version == "1.13.0" || version == "1.14.0" || version == "4.5.0" {
			markupContract = contract.ConnectMarkupInterface
			markupFixture = "apps-connect-source-native-v4.json"
		}
		if version == "1.15.0" || version == "1.16.0" || version == "4.7.0" {
			markupContract = contract.SettingsMarkupInterface
			markupFixture = "apps-settings-source-native-v5.json"
		}
		if version == "1.19.0" || version == "1.20.0" || version == "4.11.0" {
			markupContract = contract.PagesMarkupInterface
			markupFixture = "apps-pages-native-v7.json"
		}
		if version == "1.17.0" || version == "1.18.0" || version == "4.9.0" {
			markupContract = contract.DirectoryControlsMarkupInterface
			markupFixture = "apps-directory-controls-native-v6.json"
		}
		if version == "0.4.0" || version == "3.2.0" || version == "3.3.0" || version == "3.5.0" {
			markupContract = contract.MarkupInterface
			markupFixture = "apps-markup-native-v1.json"
		}
		if version == "4.2.0" {
			markupFixture = "apps-markup-native-v1.json"
		}
		raw, err := os.ReadFile("../../tests/fixtures/plugins/" + markupFixture)
		if err != nil {
			t.Fatal(err)
		}
		if version == "1.10.0" || version == "4.3.0" {
			var source contract.AccountMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			if version == "1.10.0" {
				source.Account = strings.Replace(source.Account, "{{.Name}} · {{.Alias}}", "{{.Alias}} · {{.Name}}", 1)
			} else {
				source.Account = strings.Replace(source.Account, "{{.Buttons}}", `{{.Buttons}}<button name="action" value="disconnect">Unexpected</button>`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "3.2.0" || version == "3.3.0" {
			var source contract.MarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			if version == "3.2.0" {
				source.Card = strings.Replace(source.Card, `{{.Name}}`, `{{.Apps.State.KeyRef}}`, 1)
			} else {
				source.Card = strings.Replace(source.Card, `name="action" value="connect"`, `name="action" value="disconnect"`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.6.0" {
			var source contract.GraphicMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			source.Card = strings.Replace(source.Card, `<span class="app-copy">`, `<div class="app-copy" data-plugin-copy="v1.6">`, 1)
			source.Card = strings.Replace(source.Card, `"app-card-atom-end-copy"}}</span>`, `"app-card-atom-end-copy"}}</div>`, 1)
			source.Sidebar = strings.Replace(source.Sidebar, `<span>{{.Name}}</span>`, `<span data-plugin-sidebar="v1.6">{{.Name}}</span>`, 1)
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.8.0" || version == "4.0.0" {
			var source contract.GraphicMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			if version == "1.8.0" {
				source.Graphics = graphicVariantSource(source.Graphics)
			} else {
				source.Graphics = strings.Replace(source.Graphics, "<svg ", `<svg onload="alert(1)" `, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.20.0" || version == "4.11.0" {
			var source contract.PageMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			if version == "1.20.0" {
				source.Pages = strings.Replace(source.Pages, `<h2>{{.Name}}</h2><p>{{.Description}}</p>`, `<p>{{.Description}}</p><h2>{{.Name}}</h2>`, 1)
			} else {
				source.Pages = strings.Replace(source.Pages, `method="get"`, `method="post"`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.18.0" || version == "4.9.0" {
			var source contract.DirectoryMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			// Owner literals are authored independently from candidate mutation.
			if version == "1.18.0" {
				first := `<a href="/settings/apps" {{if .DiscoverCurrent}}aria-current="page"{{end}}>{{.Discover}}</a>`
				second := `<a href="/settings/apps?view=connected" {{if .ConnectedCurrent}}aria-current="page"{{end}}>{{.YourApps}}</a>`
				third := `<a href="/settings/apps?view=all" {{if .DirectoryCurrent}}aria-current="page"{{end}}>{{.AllApps}}</a>`
				if !strings.Contains(source.Directory, first+second+third) {
					t.Fatal("original Owner navigation oracle missing")
				}
				source.Directory = strings.Replace(source.Directory, first+second+third, third+second+first, 1)
			} else {
				source.Directory = strings.Replace(source.Directory, `method="get"`, `method="post"`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.16.0" || version == "4.7.0" {
			var source contract.SettingsMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			// Independent Owner literal variation; never call candidate mutation.
			if version == "1.16.0" {
				source.Settings.English.SaveLabel = "Save connection service"
				source.Settings.Chinese.SaveLabel = "保存连接服务"
			} else {
				source.Settings.Program = strings.Replace(source.Settings.Program, `name="key_ref"`, `name="api_key"`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		if version == "1.14.0" || version == "4.5.0" {
			var source contract.ConnectMarkupSource
			if err := json.Unmarshal(raw, &source); err != nil {
				t.Fatal(err)
			}
			// Owner variation is explicit and independent of the candidate builder.
			if version == "1.14.0" {
				source.Connect.English.AccountLabel = "Account name"
				source.Connect.Chinese.AccountLabel = "账户名称"
			} else {
				source.Connect.Program = strings.Replace(source.Connect.Program, `name="alias"`, `name="key_ref"`, 1)
			}
			raw, _ = json.MarshalIndent(source, "", "  ")
			raw = append(raw, '\n')
		}
		expected, _ := json.Marshal(contract.Markup{Resource: contract.MarkupResource, Digest: rsiplugin.FileDigest(raw), Source: string(raw)})
		policy.AllowedContracts = append(policy.AllowedContracts, markupContract)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: markupContract, Input: json.RawMessage(`{}`), Expected: expected})
		if version == "4.2.0" {
			policy.AllowedContracts = append(policy.AllowedContracts, contract.MarkupInterface)
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: contract.MarkupInterface, Input: json.RawMessage(`{}`), Expected: expected})
		}
	}

	if version != "3.4.0" && version != "2.9.0" && version != "0.7.0" && version != "0.8.0" && version != "0.9.0" {
		var program []string
		native, err := os.ReadFile("../../tests/fixtures/plugins/apps-card-program-native-v1.json")
		if err != nil {
			t.Fatal("interface fixture prepare/install/policy", version, err)
		}
		if err = json.Unmarshal(native, &program); err != nil {
			t.Fatal("interface fixture prepare/install/policy", version, err)
		}
		if version == "1.4.0" {
			program = []string{"begin-card", "begin-summary", "begin-copy", "name", "description", "end-copy", "icon", "end-summary", "manage", "connect", "end-card"}
		}
		resource, err := os.ReadFile(filepath.Join(host.Catalog.Path(pkg.Digest), contract.CardViewFile))
		if err != nil {
			t.Fatal("interface fixture prepare/install/policy", version, err)
		}
		expected, _ := json.Marshal(contract.CardView{Resource: contract.CardViewResource, Digest: rsiplugin.FileDigest(resource), Program: program})
		policy.AllowedContracts = append(policy.AllowedContracts, contract.CardViewInterface)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: contract.CardViewInterface, Input: json.RawMessage(`{}`), Expected: expected})
	}
	if version != "3.4.0" && version != "0.6.0" && version != "0.7.0" && version != "0.8.0" && version != "0.9.0" && version != "2.8.0" {
		var program []string
		native, err := os.ReadFile("../../tests/fixtures/plugins/apps-sidebar-program-native-v1.json")
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(native, &program); err != nil {
			t.Fatal(err)
		}
		if version == "1.5.0" {
			program = []string{"begin-entry", "name", "icon", "pending", "end-entry"}
		}
		resource, err := os.ReadFile(filepath.Join(host.Catalog.Path(pkg.Digest), contract.SidebarViewFile))
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(contract.SidebarView{Resource: contract.SidebarViewResource, Digest: rsiplugin.FileDigest(resource), Program: program})
		policy.AllowedContracts = append(policy.AllowedContracts, contract.SidebarViewInterface)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: contract.SidebarViewInterface, Input: json.RawMessage(`{}`), Expected: expected})
	}
	add := func(language string, legacy bool) {
		if version == "2.8.0" || version == "2.9.0" || version == "3.4.0" {
			return
		}
		profileFile := "apps-interface-profile-native-v1.json"
		compositionFile := "apps-composition-order-native-v1.json"
		if language == "zh-CN" {
			profileFile = "apps-interface-profile-native-zh-v1.json"
			compositionFile = "apps-composition-native-zh-v1.json"
		}
		load := func(name string, value any) {
			t.Helper()
			raw, err := os.ReadFile("../../tests/fixtures/plugins/" + name)
			if err != nil {
				t.Fatal("interface fixture prepare/install/policy", version, err)
			}
			if err := json.Unmarshal(raw, value); err != nil {
				t.Fatal("interface fixture prepare/install/policy", version, err)
			}
		}
		var p contract.Profile
		var c contract.Composition
		load(profileFile, &p)
		load(compositionFile, &c)
		if version == "1.6.0" {
			p.Accounts[32].StatusLabel = "Workspace"
		}
		if version == "1.1.0" {
			for i := range p.Accounts {
				slices.Reverse(p.Accounts[i].Actions)
			}
			p.Browser.PollIntervalMS = 10000
		}
		if version == "1.2.0" {
			c.Service = []string{"heading", "project", "back"}
			c.Copy.ServiceTitle = "Connection service"
		}
		if version == "1.3.0" {
			c.Copy.ServiceTitle = "Connection workspace"
			p.Browser.Messages.AuthorizationOpened = "Finish linking in the authorization tab."
			p.Browser.PollNotices[1].Text = "Account ready."
			p.Notices.AccountSaved = "Ready"
			p.Errors[2] = "Input is invalid."
			if language == "zh-CN" {
				c.Copy.ServiceTitle = "Workspace"
				p.Browser.Messages.AuthorizationOpened = "请在授权标签页完成连接。"
				p.Browser.PollNotices[1].Text = "Workspace"
				p.Notices.AccountSaved = "Workspace"
				p.Errors[2] = "Workspace"
			}
		}
		profileContract, compositionContract := contract.LocalizedInterface, contract.LocalizedComposition
		input, _ := json.Marshal(contract.LocaleRequest{Locale: language})
		if legacy {
			profileContract = contract.Interface
			compositionContract = contract.CompositionInterface
			input = json.RawMessage(`{}`)
		}
		output, _ := json.Marshal(p)
		if version != "2.4.0" {
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: profileContract, Input: input, Expected: output})
		}
		if version != "2.4.0" && !slices.Contains(policy.AllowedContracts, profileContract) {
			policy.AllowedContracts = append(policy.AllowedContracts, profileContract)
		}
		if version != "0.9.0" {
			output, _ = json.Marshal(c)
			if version == "1.19.0" || version == "1.20.0" || version == "4.11.0" || version == "1.18.0" || version == "4.9.0" || version == "1.17.0" || version == "1.16.0" || version == "4.7.0" || version == "1.15.0" || version == "1.11.0" || version == "1.12.0" || version == "1.13.0" || version == "1.14.0" || version == "4.5.0" {
				compositionContract = contract.AccountCompositionInterface
				output, _ = json.Marshal(contract.AccountComposition{Composition: c, AccountCopy: accountCopyOwnerFixture(t, language, version)})
			}
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-interface", Contract: compositionContract, Input: input, Expected: output})
			if !slices.Contains(policy.AllowedContracts, compositionContract) {
				policy.AllowedContracts = append(policy.AllowedContracts, compositionContract)
			}
		}
	}
	legacy := version == "0.9.0" || version == "0.8.0"
	add("en", legacy)
	if !legacy {
		add("zh-CN", false)
	}
	if version == "2.2.0" || version == "2.3.0" {
		add("en", true)
	}
	if len(accounts) != 0 {
		p, err := host.Store.Policy(ctx, "accounts-owner-1.0.0")
		if err != nil {
			t.Fatal("interface fixture prepare/install/policy", version, err)
		}
		policy.AllowedContracts = append(policy.AllowedContracts, p.AllowedContracts...)
		policy.AllowedRequests = append(policy.AllowedRequests, p.AllowedRequests...)
		policy.Cases = append(policy.Cases, p.Cases...)
	}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal("interface fixture prepare/install/policy", version, err)
	}
	return graph.Revision, policy.ID
}

func activateInterface(t *testing.T, ctx context.Context, host rsi.Host, baseline, target, policy, operation string) {
	t.Helper()
	if err := host.Propose(ctx, rsi.Candidate{ID: operation, Baseline: baseline, Target: target, PolicyID: policy, Goal: "Independently version actual Apps SSR/browser rules with original controls and accepted effects"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: operation, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
}

func TestAppsInterfaceActualProfileFrozenEffectsColdAndUnavailable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-interface-owner-atleast24"
	if err := secrets.Put(ctx, "credential:interface-owner", []byte(token)); err != nil {
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
	var host rsi.Host
	var server *web.Server
	reads := 0
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		server = &web.Server{Config: web.Config{TokenRef: "credential:interface-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps, Interface: func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
			reads++
			return a.appsInterfaceWithHost(c, host, reference, locale)
		}}
		a.connectedApps.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) { return a.appsSettingsWithHost(c, host) }
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.Changed = nil
	}
	bind()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	seed := func() {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.UserID = "owner"
		state.Connections = []connectedapps.Connection{{ID: "keeper", Toolkit: "gmail", Name: "Gmail", Alias: "work", AccountID: "ca_keeper", Status: "active"}, {ID: "closing", Toolkit: "gmail", Name: "Gmail", Alias: "public", Public: true, Status: "active"}}
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	oldGraph, _ := activateAccountIntent(t, ctx, host, "", "1.0.0")
	original := get("/settings/apps?app=gmail")
	if original.Code != 200 {
		t.Fatal(original.Code)
	}
	old, err := a.appsInterfaceWithHost(ctx, host, "", "en")
	if err != nil {
		t.Fatal(err)
	}
	first, firstPolicy := prepareInterface(t, ctx, host, "1.0.0", oldGraph)
	activateInterface(t, ctx, host, oldGraph, first, firstPolicy, "interface-first")
	currentReader := server.Interface
	server.Interface = func(context.Context, string, string) (web.AppInterfacePolicy, error) { return old, nil }
	if w := get("/settings/apps?app=gmail"); w.Code != 200 || !strings.Contains(w.Body.String(), `data-app-interface-mode="native"`) {
		t.Fatal("captured native interpretation changed")
	}
	server.Interface = currentReader
	w := get("/settings/apps?app=gmail")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-app-interface-mode="managed"`) || !strings.Contains(w.Body.String(), first) {
		t.Fatal("actual managed profile missing", w.Code)
	}
	second, secondPolicy := prepareInterface(t, ctx, host, "1.1.0", oldGraph)
	spy := &accountCloseStore{Store: a.store, afterSave: func(n int) {
		if n == 1 {
			activateInterface(t, ctx, host, first, second, secondPolicy, "interface-second")
		}
	}}
	a.connectedApps.Store = spy
	values := url.Values{"action": {"disconnect"}, "connection": {"closing"}, "interface_revision": {first}}
	post := func(values url.Values, jsonReply bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization", "Bearer "+token)
		if jsonReply {
			r.Header.Set("Accept", "application/json")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	reads = 0
	accepted := post(values, true)
	if accepted.Code != 200 || reads != 1 || spy.saves != 2 {
		t.Fatal("accepted closure hidden or late UI selector", accepted.Code, reads, spy.saves, accepted.Body.String())
	}
	var reply map[string]string
	if err := json.Unmarshal(accepted.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	check := strings.Index(reply["accounts_html"], `value="check"`)
	disconnect := strings.Index(reply["accounts_html"], `value="disconnect"`)
	if check < 0 || disconnect < check || !strings.Contains(reply["accounts_html"], first) || strings.Contains(reply["accounts_html"], second) {
		t.Fatal("accepted response did not retain original profile", reply["accounts_html"])
	}
	w = get("/settings/apps?app=gmail")
	if w.Code != 200 || strings.Index(w.Body.String(), `value="disconnect"`) > strings.Index(w.Body.String(), `value="check"`) {
		t.Fatal("independent control order not used", w.Code)
	}
	if w := get("/settings/apps?app=gmail&interface_revision=" + url.QueryEscape(first)); w.Code != 200 || strings.Index(w.Body.String(), `value="check"`) > strings.Index(w.Body.String(), `value="disconnect"`) {
		t.Fatal("old profile reference changed")
	}
	// The native follow-up must reuse the same pre-effect profile too: loadApps
	// cannot read the now-current version after the real account save.
	activateInterface(t, ctx, host, second, first, firstPolicy, "interface-native-baseline")
	seed()
	spy = &accountCloseStore{Store: a.store, afterSave: func(n int) {
		if n == 1 {
			activateInterface(t, ctx, host, first, second, secondPolicy, "interface-native-switch")
		}
	}}
	a.connectedApps.Store = spy
	reads = 0
	nativeAccepted := post(values, false)
	body := nativeAccepted.Body.String()
	if nativeAccepted.Code != 200 || reads != 1 || spy.saves != 2 || !strings.Contains(body, `data-app-interface-revision="`+first+`"`) || strings.Contains(body, second) || !strings.Contains(body, `value="check"`) || strings.Index(body, `value="check"`) > strings.Index(body, `value="disconnect"`) {
		t.Fatal("native accepted close reselected presentation", nativeAccepted.Code, reads, spy.saves, body)
	}
	// A real connection-service save is a different consumer of the frozen
	// interface. Source I/O and durable save may activate another graph.
	if err := secrets.Put(ctx, "env:COMPOSIO_API_KEY", []byte("synthetic-interface-key")); err != nil {
		t.Fatal(err)
	}
	peerCalls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/toolkits" {
			t.Errorf("unexpected service source: %s %s", r.Method, r.URL.Path)
		}
		peerCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":null}`))
	}))
	defer peer.Close()
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "env:COMPOSIO_API_KEY"
	state.Connections = nil
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	spy = &accountCloseStore{Store: a.store, afterSave: func(n int) {
		if n == 1 {
			activateInterface(t, ctx, host, second, first, firstPolicy, "interface-service-switch")
		}
	}}
	a.connectedApps.Store = spy
	form := url.Values{"revision": {strconv.FormatInt(state.Revision, 10)}, "key_ref": {state.KeyRef}, "auth_configs": {"{}"}, "interface_revision": {second}}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/service/configure", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer "+token)
	reads = 0
	serviceSaved := httptest.NewRecorder()
	server.ServeHTTP(serviceSaved, r)
	if serviceSaved.Code != 200 || reads != 1 || spy.saves != 1 || peerCalls != 1 || !strings.Contains(serviceSaved.Body.String(), "App connection service saved.") || !strings.Contains(serviceSaved.Body.String(), `name="interface_revision" value="`+second+`"`) || strings.Contains(serviceSaved.Body.String(), first) {
		t.Fatal("service save reselected presentation or lost acceptance", serviceSaved.Code, reads, spy.saves, peerCalls, serviceSaved.Body.String())
	}
	a.connectedApps.Store = a.store
	activateInterface(t, ctx, host, first, second, secondPolicy, "interface-cold-baseline")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	a.eventsRequired.Store(false)
	bind()
	if w := get("/settings/apps?app=gmail"); w.Code != 200 || !strings.Contains(w.Body.String(), second) {
		t.Fatal("cold current UI lost", w.Code)
	}
	activateInterface(t, ctx, host, second, first, firstPolicy, "interface-rollback")
	if w := get("/settings/apps?app=gmail"); w.Code != 200 || !strings.Contains(w.Body.String(), first) {
		t.Fatal("rollback UI lost", w.Code)
	}
	bad, badPolicy := prepareInterface(t, ctx, host, "2.0.0", oldGraph)
	activateInterface(t, ctx, host, first, bad, badPolicy, "interface-bad")
	if w := get("/settings/apps?app=gmail"); w.Code != 503 {
		t.Fatal("scope-forging profile restored native", w.Code)
	}
	seed()
	reads = 0
	outcome := post(url.Values{"action": {"disconnect"}, "connection": {"closing"}}, true)
	if outcome.Code != 200 || reads != 1 || !strings.Contains(outcome.Body.String(), `"presentation_unavailable":"true"`) {
		t.Fatal("unavailable UI hid independently accepted close", outcome.Code, reads, outcome.Body.String())
	}
	after, err := a.store.LoadApps(ctx)
	if err != nil || after.Connections[1].Status != "disconnected" || !after.Connections[1].RevocationConfirmed {
		t.Fatal("actual closure not durable", err)
	}
	nativeReceipt := post(url.Values{"action": {"disconnect"}, "connection": {"closing"}}, false)
	if nativeReceipt.Code != 200 || !strings.Contains(nativeReceipt.Body.String(), "The account action was accepted.") || strings.Contains(nativeReceipt.Body.String(), "data-apps ") {
		t.Fatal("native unavailable receipt hid accepted outcome", nativeReceipt.Code, nativeReceipt.Body.String())
	}
	if w := post(url.Values{"action": {"check"}, "connection": {"keeper"}, "automatic": {"1"}, "interface_revision": {first}}, true); w.Code != 200 { // first is still a retained, unblocked profile; pending/active eligibility remains the domain's decision.
		if w.Code != 422 {
			t.Fatal("retained automatic request unexpectedly reinterpreted", w.Code)
		}
	}
	if err := host.Store.Quarantine(ctx, first); err != nil {
		t.Fatal(err)
	}
	if w := post(url.Values{"action": {"check"}, "connection": {"keeper"}, "automatic": {"1"}, "interface_revision": {first}}, true); w.Code != 503 || !strings.Contains(w.Body.String(), "presentation_unavailable") {
		t.Fatal("stopped automatic work dispatched", w.Code)
	}
	activateInterface(t, ctx, host, bad, oldGraph, "accounts-owner-1.0.0", "interface-old-graph")
	if w := get("/settings/apps?app=gmail"); w.Code != 503 {
		t.Fatal("old seven-service graph restored native interface", w.Code)
	}
	if _, err := host.Store.Deactivate(ctx, "interface-disable", oldGraph); err != nil {
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
	if w := get("/settings/apps?app=gmail"); w.Code != 503 {
		t.Fatal("cold deactivated UI restored native", w.Code)
	}
}

// The actual resource changes both graphic markup and visible vector geometry.
func graphicVariantSource(source string) string {
	source = strings.Replace(source, `class="app-logo"`, `class="app-logo" data-plugin-icon="v1.8"`, 1)
	source = strings.Replace(source, `{{define "app-more"}}<svg `, `{{define "app-more"}}<svg data-plugin-more="v1.8" `, 1)
	source = strings.Replace(source, `{{define "app-plus"}}<svg `, `{{define "app-plus"}}<svg data-plugin-plus="v1.8" `, 1)
	return strings.Replace(source, `cx="5"`, `cx="4"`, 1)
}
