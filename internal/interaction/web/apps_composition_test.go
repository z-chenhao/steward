package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"html/template"
	"os"
	"strings"
	"testing"
)

func actualAppComposition(t *testing.T) contract.Composition {
	return actualAppCompositionLocale(t, "en")
}
func actualAppCompositionLocale(t *testing.T, locale string) contract.Composition {
	t.Helper()
	var composition contract.AccountComposition
	raw := actualAppsPresentationResult(t, contract.AccountCompositionInterface, json.RawMessage(`{"locale":"`+locale+`"}`))
	if err := rsiplugin.DecodeResult(raw, &composition); err != nil {
		t.Fatal(err)
	}
	return composition.Composition
}

type appCompositionScene struct {
	Mode, Language string
	Flags          int
}

func appCompositionNativeScene(f appCompositionScene) (page, string) {
	state := connectedapps.State{Revision: 7, UserID: "private-owner-must-not-render", KeyRef: "credential:reference_only"}
	row := connectedapps.Connection{ID: "original_account", Toolkit: "sample_app", Name: "Original <App>", Alias: "工作 & 个人", AccountID: "private-account-id-must-not-render", Status: "pending"}
	if f.Flags&1 != 0 {
		row.Status = "active"
	}
	state.Connections = []connectedapps.Connection{row}
	card := appCard{Toolkit: composio.Toolkit{Slug: "sample_app", Name: "Original <App>", NoAuth: f.Flags&2 != 0}, Connected: f.Flags&1 != 0, Managed: true, Description: "用途 & <description>", URL: "/settings/apps?app=sample_app", Initial: "O"}
	view := &appsView{View: connectedapps.View{State: state}, ConnectionID: "original_account", ConnectURL: "https://app.composio.dev/link/original", ExpiresAt: 123456, Groups: []appGroup{{Title: "Original <Group>", URL: "/settings/apps?collection=original", Items: []appCard{card}}}, Managed: []appCard{card}, AuthConfigRows: []appAuthConfigRow{{App: "gmail", Config: "ac_original"}, {App: "raw <draft>", Config: "保留 & 原值"}}}
	if f.Flags&4 != 0 {
		view.Groups = nil
		view.Managed = nil
		view.Unavailable = true
	}
	if f.Flags&8 != 0 {
		view.Categories = []composio.Category{{ID: "original_category", Name: "Original <Category>"}}
		view.Category = "original_category"
		view.Search = "保留 & <query>"
		view.Catalog.NextCursor = "page+2=&"
	}
	name := "apps-page"
	switch f.Mode {
	case "all":
		view.Directory = true
	case "connected":
		view.ConnectedOnly = true
	case "collection":
		view.Collection = "original"
	case "detail":
		view.Selected = &card
	case "service":
		name = "app-service-page"
	case "sidebar":
		name = "app-sidebar"
	}
	p := page{Apps: view}
	if f.Flags&1 != 0 {
		p.Notice = "Unsaved changes."
	}
	if f.Flags&2 != 0 {
		p.Error = "Original validation failure"
	}
	return p, name
}
func TestAppsCompositionManagedOriginal224PagesAndSidebar(t *testing.T) {
	var cases []struct {
		Scene    appCompositionScene
		Expected struct {
			SHA256 string
			Bytes  int
		}
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-composition-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 224 {
		t.Fatal("original composition expectations changed")
	}
	policies := actualLocalizedAppPolicies(t)
	for _, mode := range []string{"native", "managed"} {
		for i, f := range cases {
			t.Run(fmt.Sprintf("%s/%s/%s/%d", mode, f.Scene.Mode, f.Scene.Language, i), func(t *testing.T) {
				p, name := appCompositionNativeScene(f.Scene)
				if mode == "managed" {
					managed := policies[f.Scene.Language]
					p.Apps.Interface = &managed
				}
				var out bytes.Buffer
				if err := requestLayout(languageRequest("GET", "/settings/apps", f.Scene.Language, "")).ExecuteTemplate(&out, name, p); err != nil {
					t.Fatal(err)
				}
				body := out.String()
				if mode == "managed" {
					ref := `<input type="hidden" name="interface_revision" value="fixture-original">`
					if strings.Count(body, `name="interface_revision"`) != strings.Count(body, ref) {
						t.Fatal("unproved reference")
					}
					body = strings.ReplaceAll(body, ref, "")
					if name == "apps-page" {
						owned := ` data-app-interface-localized="true"`
						if strings.Count(body, owned) != 1 {
							t.Fatal("unproved owned-text metadata")
						}
						body = strings.Replace(body, owned, "", 1)
						attrs := `data-app-interface-mode="managed" data-app-interface-revision="fixture-original" data-app-interface-profile="` + template.HTMLEscapeString(p.Apps.InterfaceJSON()) + `"`
						if strings.Count(body, attrs) != 1 {
							t.Fatal("original exact profile identity missing")
						}
						body = strings.Replace(body, attrs, `data-app-interface-mode="native" data-app-interface-revision="" data-app-interface-profile=""`, 1)
					}
					if f.Scene.Mode == "detail" {
						attrs := fmt.Sprintf(` data-app-no-auth="%t" data-app-suggested="false"`, f.Scene.Flags&2 != 0)
						if strings.Count(body, attrs) != 1 {
							t.Fatal("original detail facts changed")
						}
						body = strings.Replace(body, attrs, "", 1)
					}
				}
				if strings.Contains(body, "private-owner-must-not-render") || strings.Contains(body, "private-account-id-must-not-render") {
					t.Fatal("private facts escaped their original scope")
				}
				sum := sha256.Sum256([]byte(body))
				if len(body) != f.Expected.Bytes || hex.EncodeToString(sum[:]) != f.Expected.SHA256 {
					t.Fatalf("original composition changed: got %s/%d want %s/%d", hex.EncodeToString(sum[:]), len(body), f.Expected.SHA256, f.Expected.Bytes)
				}
			})
		}
	}
}
func TestAppsCompositionRejectsMissingDuplicateAndUnknownSections(t *testing.T) {
	profile := actualInterfaceProfile(t)
	base := actualAppComposition(t)
	raw, _ := json.Marshal(base)
	for name, change := range map[string]func(*contract.Composition){
		"empty-title":            func(c *contract.Composition) { c.Copy.ServiceTitle = " " },
		"missing-close-controls": func(c *contract.Composition) { c.Account = c.Account[:1] },
		"duplicate-identity":     func(c *contract.Composition) { c.Account[1] = "copy" },
		"hidden-body":            func(c *contract.Composition) { c.Page[4] = "feedback" },
		"unknown-template":       func(c *contract.Composition) { c.Service[2] = "layout.html" },
	} {
		t.Run(name, func(t *testing.T) {
			var c contract.Composition
			_ = json.Unmarshal(raw, &c)
			change(&c)
			p, err := ManagedAppInterface("fixture-original", profile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ManagedAppComposition(p, c); err == nil {
				t.Fatal("unsafe composition accepted")
			}
		})
	}
}

func TestAppsCompositionPreservesCopyEscapingAndOriginalFields(t *testing.T) {
	profile := actualInterfaceProfile(t)
	composition := actualAppComposition(t)
	composition.Copy.ServiceTitle = `<script>alert("candidate")</script>`
	p, err := ManagedAppInterface("fixture-original", profile)
	if err != nil {
		t.Fatal(err)
	}
	p, err = ManagedAppComposition(p, composition)
	if err != nil {
		t.Fatal(err)
	}
	view, name := appCompositionNativeScene(appCompositionScene{Mode: "service", Language: "en"})
	view.Apps.Interface = &p
	var out bytes.Buffer
	if err := requestLayout(languageRequest("GET", "/settings/apps/service", "en", "")).ExecuteTemplate(&out, name, view); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `name="key_ref" value="credential:reference_only"`) || !strings.Contains(body, `name="app_slug" value="raw &lt;draft&gt;"`) {
		t.Fatal("copy became code or rewrote original fields", body)
	}
}
