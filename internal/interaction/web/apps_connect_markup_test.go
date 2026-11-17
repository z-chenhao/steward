package web

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func connectViewSource(t *testing.T) (contract.ConnectMarkupSource, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../../plugins/apps-interface/views/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.ConnectMarkupSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	return source, raw
}

func TestAppConnectMarkupRejectsLostAuthorityAndChangedAliasControls(t *testing.T) {
	source, _ := connectViewSource(t)
	if _, err := compileAppConnectMarkup(source.Connect.Program); err != nil {
		t.Fatal("original connection controls rejected", err)
	}
	for name, change := range map[string][2]string{
		"lost-identity":      {"{{.Identity}}", ""},
		"duplicate-identity": {"{{.Identity}}", "{{.Identity}}{{.Identity}}"},
		"lost-help":          {"{{.Help}}", ""},
		"external-post":      {"action=\"/apps/configure\"", "action=\"https://example.invalid\""},
		"get-effect":         {"method=\"post\"", "method=\"get\""},
		"alias-meaning":      {"name=\"alias\"", "name=\"key_ref\""},
		"alias-length":       {"maxlength=\"64\"", "maxlength=\"65\""},
		"alias-required":     {"maxlength=\"64\" required", "maxlength=\"64\""},
		"alias-pattern":      {"pattern=\"[A-Za-z0-9][A-Za-z0-9_-]{0,63}\"", "pattern=\".*\""},
		"extra-secret-input": {"{{.Identity}}", "{{.Identity}}<input name=\"credential\">"},
		"hidden-first":       {"{{if .FirstHidden}}hidden{{end}}", "hidden"},
		"disabled-submit":    {"<button type=\"submit\">", "<button type=\"submit\" disabled>"},
		"script":             {"<summary>", "<script>PRIVATE()</script><summary>"},
		"event":              {"<summary>", "<summary onclick=\"PRIVATE()\">"},
		"internal-state":     {"{{.FirstLabel}}", "{{.Apps.State.KeyRef}}"},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := strings.Replace(source.Connect.Program, change[0], change[1], 1)
			if invalid == source.Connect.Program {
				t.Fatal("negative fixture did not change source")
			}
			if _, err := compileAppConnectMarkup(invalid); err == nil {
				t.Fatal("candidate changed original form authority/validation")
			}
		})
	}
}

func TestAppConnectMarkupOriginal32ScenesBothLocalesAndNoCurrentFallback(t *testing.T) {
	_, raw := connectViewSource(t)
	for _, language := range []string{"en", "zh-CN"} {
		load := func(name string, v any) {
			t.Helper()
			raw, err := os.ReadFile("../../../tests/fixtures/plugins/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, v); err != nil {
				t.Fatal(err)
			}
		}
		profileFile, copyFile := "apps-interface-profile-native-v1.json", "apps-composition-order-native-v1.json"
		if language == "zh-CN" {
			profileFile, copyFile = "apps-interface-profile-native-zh-v1.json", "apps-composition-native-zh-v1.json"
		}
		var profile contract.Profile
		var composition contract.Composition
		load(profileFile, &profile)
		load(copyFile, &composition)
		p, err := ManagedLocalizedAppInterface("original_graph", profile)
		if err != nil {
			t.Fatal(err)
		}
		p, err = ManagedLocalizedAppComposition(p, composition)
		if err != nil {
			t.Fatal(err)
		}
		p = actualCurrentAppMarkup(t, p, contract.Markup{Source: string(raw)})
		host := newLayout(language)
		for flags := 0; flags < 32; flags++ {
			view := &appsView{Interface: &p, Selected: &appCard{Toolkit: composio.Toolkit{Slug: "original_app", NoAuth: flags&2 != 0}, Connected: flags&4 != 0, PublicActive: flags&8 != 0, Managed: flags&16 != 0}}
			view.Suggested = flags&1 != 0
			for _, part := range []string{"first", "additional"} {
				got, err := appConnectPart(host, view, part, language)
				if err != nil {
					t.Fatal(language, flags, part, err)
				}
				var original bytes.Buffer
				if err := host.ExecuteTemplate(&original, "app-detail-connect-"+part+"-legacy-v3", page{Apps: view}); err != nil {
					t.Fatal(err)
				}
				if string(got) != original.String() {
					t.Fatalf("original form changed language=%s flags=%d part=%s\ngot=%s\nwant=%s", language, flags, part, got, original.String())
				}
			}
		}
		p.connectMarkup = nil
		view := &appsView{Interface: &p, Selected: &appCard{Toolkit: composio.Toolkit{Slug: "original_app"}}}
		if out, err := appConnectPart(host, view, "first", language); err == nil || out != "" {
			t.Fatal("current unavailable contribution restored native form")
		}
	}
}
