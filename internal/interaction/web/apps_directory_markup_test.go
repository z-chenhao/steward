package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
)

func TestAppDirectoryMarkupOriginalNavigationFilterBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-directory-controls-source-v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Directory string `json:"directory"`
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileAppDirectoryMarkup(source.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN"} {
		for flags := 0; flags < 4; flags++ {
			for _, collection := range []string{"", "communication"} {
				for _, count := range []int{0, 1, 3} {
					for _, cursor := range []string{"", `next<&"/? 中文`} {
						t.Run(fmt.Sprintf("%s/%d/%s/%d/%t", locale, flags, collection, count, cursor != ""), func(t *testing.T) {
							v := &appsView{View: connectedapps.View{Search: `search<&" 中文`, Category: "selected_category"}, ConnectedOnly: flags&1 != 0, Directory: flags&2 != 0, Collection: collection}
							v.Catalog.NextCursor = cursor
							for i := 0; i < count; i++ {
								id := fmt.Sprint("category_", i)
								if i == count-1 {
									id = v.Category
								}
								v.Categories = append(v.Categories, composio.Category{ID: id, Name: `Name<&" 中文`})
							}
							copy := v.Copy()
							text := appCopyText(locale)
							d := appDirectoryMarkupData{AppViews: text(v, copy.AppViews), Discover: text(v, copy.Discover), YourApps: text(v, copy.YourApps), AllApps: text(v, copy.AllApps), BackToApps: text(v, copy.BackToApps), CategoryLabel: text(v, copy.Category), AllCategories: text(v, copy.AllCategories), Filter: text(v, copy.Filter), MoreApps: text(v, copy.MoreApps), Search: v.Search, Category: v.Category, NextCursor: cursor, DiscoverCurrent: !v.ConnectedOnly && !v.Directory, ConnectedCurrent: v.ConnectedOnly, DirectoryCurrent: v.Directory, CollectionPresent: collection != ""}
							host := newLayout(locale)
							for _, part := range []string{"tabs", "collection-back", "categories", "pagination"} {
								actual, err := renderAppDirectoryMarkup(compiled, part, d, v.Categories, locale)
								if err != nil {
									t.Fatal(err)
								}
								var expected bytes.Buffer
								if err := host.ExecuteTemplate(&expected, "app-directory-"+part+"-legacy-v5", page{Apps: v}); err != nil {
									t.Fatal(err)
								}
								if string(actual) != expected.String() {
									t.Fatalf("%s original controls changed\nactual:%s\nexpected:%s", part, actual, expected.String())
								}
							}
						})
					}
				}
			}
		}
	}
}

func TestAppDirectoryMarkupRejectsChangedNavigationAndFilterControls(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-directory-controls-source-v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct{ Directory string }
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string][2]string{
		"external-navigation":      {`href="/settings/apps"`, `href="https://example.invalid"`},
		"changed-current-tab":      {"{{if .ConnectedCurrent}}", "{{if .DiscoverCurrent}}"},
		"effectful-filter":         {`method="get"`, `method="post"`},
		"changed-view":             {`name="view" value="all"`, `name="view" value="connected"`},
		"lost-search":              {`<input type="hidden" name="search" value="{{.Search}}">`, ""},
		"extra-field":              {"{{.Options}}", `{{.Options}}<input name="owner_scope" value="other">`},
		"changed-category":         {`name="category"`, `name="app"`},
		"changed-select-identity":  {`id="app-category"`, `id="another-category"`},
		"changed-option-id":        {"{{.OptionID}}", "fabricated_id"},
		"changed-option-selection": {"{{if .OptionSelected}}selected{{end}}", "selected"},
		"lost-options":             {"{{.Options}}", ""},
		"duplicate-options":        {"{{.Options}}", "{{.Options}}{{.Options}}"},
		"disabled-fallback":        {`class="secondary"`, `class="secondary" disabled`},
		"extra-noscript-input":     {`</noscript>`, `<input name="secret"></noscript>`},
		"lost-cursor":              {`&amp;cursor={{.NextCursor}}`, ""},
		"changed-cursor":           {"{{.NextCursor}}", "{{.Search}}"},
		"extra-query":              {`&amp;cursor={{.NextCursor}}`, `&amp;cursor={{.NextCursor}}&amp;owner_scope=other`},
		"duplicate-query":          {`&amp;cursor={{.NextCursor}}`, `&amp;cursor={{.NextCursor}}&amp;search=other`},
		"script":                   {`<nav`, `<script>alert(1)</script><nav`},
		"event":                    {`<nav`, `<nav onclick="evil()"`},
		"hidden-navigation":        {`<nav`, `<div hidden></div><nav`},
		"private-state":            {"{{.Search}}", "{{.State.Search}}"},
		"candidate-loop":           {"{{.Options}}", "{{range .Options}}{{.}}{{end}}"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(source.Directory, change[0], change[1], 1)
			if bad == source.Directory {
				t.Fatal("negative did not change source")
			}
			if _, err := compileAppDirectoryMarkup(bad); err == nil {
				t.Fatal("changed original navigation accepted")
			}
		})
	}
}

func TestAppDirectoryMarkupActualDefaultSourceAndNoFallback(t *testing.T) {
	policies := actualLocalizedAppPolicies(t)
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			policy := policies[locale]
			if !policy.directoryManaged || policy.directoryMarkup == nil {
				t.Fatal("actual default did not attach directory source")
			}
			v := &appsView{Interface: &policy, View: connectedapps.View{Search: `original<&" 中文`, Category: "original_category", Categories: []composio.Category{{ID: "other", Name: "Other"}, {ID: "original_category", Name: "Communication"}}}, Directory: true, Collection: "communication"}
			v.Catalog.NextCursor = `next<&"/ 中文`
			host := newLayout(locale)
			for _, part := range []string{"tabs", "collection-back", "categories", "pagination"} {
				actual, err := appDirectoryPart(host, v, part, locale)
				if err != nil {
					t.Fatal(err)
				}
				var expected bytes.Buffer
				if err := host.ExecuteTemplate(&expected, "app-directory-"+part+"-legacy-v5", page{Apps: v}); err != nil {
					t.Fatal(err)
				}
				if string(actual) != expected.String() {
					t.Fatal("actual contribution changed original navigation", part)
				}
			}
			policy = UnavailableAppDirectoryMarkup(policy)
			for _, part := range []string{"tabs", "collection-back", "categories", "pagination"} {
				if _, err := appDirectoryPart(host, v, part, locale); err == nil {
					t.Fatal("unavailable current contribution restored legacy", part)
				}
			}
		})
	}
}
