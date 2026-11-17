package web

import (
	"bytes"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"os"
	"strings"
	"testing"
)

func TestAppPagesSourceHasOnlyDisplayFields(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-pages-source-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Program string `json:"program"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if _, err := parseAppPagesMarkup(source.Program); err != nil {
		t.Fatalf("original pages: %v", err)
	}
	for name, changed := range map[string]string{
		"domain state":          strings.Replace(source.Program, "{{.Category}}", "{{.State.KeyRef}}", 1),
		"unknown display value": strings.Replace(source.Program, "{{.Name}}", "{{.Credential}}", 1),
		"method":                strings.Replace(source.Program, "{{.Name}}", "{{.Name.String}}", 1),
		"function":              strings.Replace(source.Program, "{{.Name}}", "{{printf `%v` .Name}}", 1),
		"candidate iteration":   strings.Replace(source.Program, "{{.Name}}", "{{range .Accounts}}{{.}}{{end}}", 1),
		"child invocation":      strings.Replace(source.Program, "{{.Name}}", "{{template `help` .}}", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAppPagesMarkup(changed); err == nil {
				t.Fatal("accepted source outside private display seam")
			}
		})
	}
}

func TestAppPagesOriginalDisplayBytes(t *testing.T) {
	originalSource, err := os.ReadFile("../../../tests/fixtures/plugins/apps-interface-pre-pages-v11/apps.html")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-pages-source-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Program string }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileAppPagesMarkup(source.Program)
	if err != nil {
		t.Fatal(err)
	}
	policies := actualLocalizedAppPolicies(t)
	for _, language := range []string{"en", "zh-CN"} {
		policy := policies[language]
		for _, selectedPolicy := range []*AppInterfacePolicy{nil, &policy} {
			for flags := 0; flags < 32; flags++ {
				v := &appsView{Interface: selectedPolicy, ConnectedOnly: flags&1 != 0}
				v.Unavailable = flags&2 != 0
				v.Search = `mail <&" 中文`
				v.Category = `category <&" 中文`
				if flags&4 != 0 {
					v.Selected = &appCard{Toolkit: composio.Toolkit{Slug: "gmail", Name: `Gmail <&" 中文`}, Description: `Description <&" 中文`}
				}
				if flags&8 != 0 {
					card := appCard{Toolkit: composio.Toolkit{Slug: "gmail", Name: `Gmail <&" 中文`}, Description: `Description <&" 中文`, URL: "/settings/apps?app=gmail", Managed: true}
					v.Managed = []appCard{card}
					v.Groups = []appGroup{{Title: `Group <&" 中文`, URL: "/settings/apps?collection=communication", Items: []appCard{card}}, {Title: "No link", Items: []appCard{card}}}
					v.State.Connections = []connectedapps.Connection{{ID: "account-original", Toolkit: "gmail", Name: `Account <&" 中文`, Alias: "work", Status: "active", Public: flags&16 != 0}}
				}
				p := page{Apps: v}
				host := newLayout(language)
				original, err := host.Clone()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = original.Parse(string(originalSource)); err != nil {
					t.Fatal(err)
				}
				for _, part := range []string{"page-heading", "page-unavailable", "page-feedback", "page-inventory", "page-body", "page-dialog", "detail-heading", "detail-feedback", "detail-accounts", "service-heading", "service-back", "page", "service", "detail", "accounts", "sidebar", "groups"} {
					actual, err := renderAppPagesPart(host, compiled, p, part, language)
					if err != nil {
						t.Fatalf("%s/%d/%s: %v", language, flags, part, err)
					}
					var expected bytes.Buffer
					name := "app-" + part
					switch part {
					case "page":
						name = "apps-page"
					case "service":
						name = "app-service-page"
					case "groups":
						name = "app-directory-groups"
					}
					if err := original.ExecuteTemplate(&expected, name, p); err != nil {
						t.Fatal(err)
					}
					if string(actual) != expected.String() {
						t.Fatalf("%s/%d/%s changed original bytes\nactual:%s\nexpected:%s", language, flags, part, actual, expected.String())
					}
				}
			}
		}
	}
}

func TestAppPagesRejectChangedProtocolAndControls(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-pages-source-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Program string }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string][2]string{
		"script":                  {`<section class="workspace-page`, `<script>alert(1)</script><section class="workspace-page`},
		"event":                   {`<h1>`, `<h1 onclick="evil()">`},
		"remote form":             {`action="/settings/apps"`, `action="https://example.invalid"`},
		"effectful search":        {`method="get"`, `method="post"`},
		"search field":            {`name="search"`, `name="secret"`},
		"lost category":           {`name="category" value="{{.Category}}"`, `name="category" value="other"`},
		"changed view":            {`name="view" value="all"`, `name="view" value="connected"`},
		"changed search value":    {`value="{{.SearchValue}}"`, `value="invented"`},
		"missing search fallback": {`<button class="sr-only" type="submit">{{.Search}}</button>`, ``},
		"extra authority":         {`</form>`, `<input name="owner_scope" value="other"></form>`},
		"remote settings":         {`href="/settings/apps/service"`, `href="https://example.invalid"`},
		"hidden heading":          {`<h1>`, `<h1 hidden>`},
		"protocol mode":           {`data-app-interface-mode="{{.InterfaceMode}}"`, `data-app-interface-mode="native"`},
		"graph identity":          {`data-app-interface-revision="{{.InterfaceRevision}}"`, `data-app-interface-revision="other"`},
		"profile identity":        {`data-app-interface-profile="{{.InterfaceProfile}}"`, `data-app-interface-profile="{}"`},
		"locale fact":             {`{{if .Localized}}`, `{{if .ManagedPresent}}`},
		"account identity":        {`data-app-account="{{.ID}}"`, `data-app-account="{{.Toolkit}}"`},
		"fixed revision":          {`data-app-revision="{{.Revision}}"`, `data-app-revision="731"`},
		"fixed pending status":    {`data-app-state="{{.Status}}"`, `data-app-state="pending"`},
		"account status":          {`data-app-state="{{.Status}}"`, `data-app-state="active"`},
		"public fact":             {`data-app-public="{{.Public}}"`, `data-app-public="true"`},
		"missing child":           {`{{.Body}}`, ``},
		"duplicate child":         {`{{.Body}}`, `{{.Body}}{{.Body}}`},
		"wrong child":             {`{{.Detail}}`, `{{.Directory}}`},
		"hidden child":            {`{{.Detail}}`, `<span hidden>{{.Detail}}</span>`},
		"lost inventory":          {`id="app-accounts"`, `id="other"`},
		"changed empty fact":      {`{{if .EmptyHidden}}`, `{{if .Selected}}`},
		"wrong detail identity":   {`data-app-detail="{{.Slug}}"`, `data-app-detail="other"`},
		"changed auth fact":       {`data-app-no-auth="{{.NoAuth}}"`, `data-app-no-auth="true"`},
		"changed suggested fact":  {`data-app-suggested="{{.Suggested}}"`, `data-app-suggested="false"`},
		"changed group target":    {`href="{{.URL}}"`, `href="/settings/apps?collection=other"`},
		"closed dialog":           {`data-app-close aria-label`, `data-other-close aria-label`},
		"foreign graphic":         {`<svg width="14"`, `<svg onload="evil()" width="14"`},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(source.Program, change[0]) {
				t.Fatal("mutation does not reach source")
			}
			changed := strings.Replace(source.Program, change[0], change[1], 1)
			if _, err := compileAppPagesMarkup(changed); err == nil {
				t.Fatal("changed original protocol/control accepted")
			}
		})
	}
}

func TestAppPagesManagedResourceDispatchAndNoFallback(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-pages-source-v7.json")
	if err != nil {
		t.Fatal(err)
	}
	var pages struct{ Program string }
	if err = json.Unmarshal(raw, &pages); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../plugins/apps-interface/views/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.PageMarkupSource
	if err = json.Unmarshal(raw, &source.DirectoryMarkupSource); err != nil {
		t.Fatal(err)
	}
	source.Pages = pages.Program
	raw, err = json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	policies := actualLocalizedAppPolicies(t)
	for _, language := range []string{"en", "zh-CN"} {
		p, err := ManagedAppPagesMarkup(policies[language], contract.Markup{Source: string(raw)})
		if err != nil {
			t.Fatal(err)
		}
		v := &appsView{Interface: &p}
		v.State.Revision = 739
		v.Search = `original <&" 中文`
		v.Category = "communication"
		card := appCard{Toolkit: composio.Toolkit{Slug: "gmail", Name: "Gmail"}, Description: `Original <&" 中文`, URL: "/settings/apps?app=gmail", Managed: true}
		v.Managed = []appCard{card}
		v.Groups = []appGroup{{Title: "Communication", URL: "/settings/apps?collection=communication", Items: []appCard{card}}}
		v.State.Connections = []connectedapps.Connection{{ID: "original-account", Toolkit: "gmail", Name: "Gmail", Alias: "work", Status: "active"}}
		host := newLayout(language)
		for _, selected := range []bool{false, true} {
			if selected {
				v.Selected = &card
			}
			for _, part := range []string{"page", "service", "sidebar", "detail", "accounts", "groups", "page-heading", "page-unavailable", "page-feedback", "page-inventory", "page-body", "page-dialog", "detail-heading", "detail-feedback", "detail-accounts", "service-heading", "service-back"} {
				actual, err := appPagePart(host, page{Apps: v}, part, language)
				if err != nil {
					t.Fatalf("%s/%t/%s: %v", language, selected, part, err)
				}
				legacyPolicy := p
				legacyPolicy.pagesManaged = false
				legacyPolicy.pagesMarkup = nil
				legacyView := *v
				legacyView.Interface = &legacyPolicy
				expected, err := appPagePart(host, page{Apps: &legacyView}, part, language)
				if err != nil {
					t.Fatal(err)
				}
				if actual != expected {
					t.Fatalf("%s/%t/%s changed original complete render", language, selected, part)
				}
			}
		}
		p = UnavailableAppPagesMarkup(p)
		for _, part := range []string{"page", "service", "sidebar", "detail", "accounts", "groups"} {
			if _, err := appPagePart(host, page{Apps: v}, part, language); err == nil {
				t.Fatal("unavailable current page restored old default", part)
			}
		}
	}
}
