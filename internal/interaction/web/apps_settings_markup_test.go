package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"html/template"
	"os"
	"strings"
	"testing"
)

func TestAppSettingsMarkupOriginalFormBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-settings-source-v5.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.SettingsViewSource
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileAppSettingsMarkup(source.Program)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN"} {
		for _, count := range []int{0, 1, 3, 128} {
			for _, open := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/%t", locale, count, open), func(t *testing.T) {
					v := &appsView{View: connectedapps.View{State: connectedapps.State{KeyRef: `env:original<&"`, Revision: 71}}}
					v.Interface = &AppInterfacePolicy{native: true, revision: "original_graph"}
					for i := 0; i < count; i++ {
						v.AuthConfigRows = append(v.AuthConfigRows, appAuthConfigRow{App: fmt.Sprintf("raw<&%d", i), Config: `cfg"<&`})
					}
					p := page{Apps: v}
					if open {
						p.Notice = "original notice"
					}
					host := newLayout(locale)
					var expected, identity, projectHelp, keyHelp bytes.Buffer
					if err := host.ExecuteTemplate(&expected, "app-service-project-legacy-v4", p); err != nil {
						t.Fatal(err)
					}
					if err := host.ExecuteTemplate(&identity, "app-interface-reference", v); err != nil {
						t.Fatal(err)
					}
					identity.WriteString(`<input type="hidden" name="revision" value="71">`)
					copy := source.English
					if locale == "zh-CN" {
						copy = source.Chinese
					}
					title := appCopyText(locale)(v, v.Copy().ProjectTitle)
					if err := host.ExecuteTemplate(&projectHelp, "help", help("composio-project-help", title, appCopyText(locale)(v, v.Copy().ProjectHelp))); err != nil {
						t.Fatal(err)
					}
					if err := host.ExecuteTemplate(&keyHelp, "help", help("composio-key-help", translate(locale, "API key reference"), translate(locale, "Use a SecretStore or environment reference. Keep the project API key out of this form."))); err != nil {
						t.Fatal(err)
					}
					actual, err := renderAppSettingsMarkup(compiled, appSettingsMarkupData{SettingsCopy: copy, ProjectTitle: title, AuthenticationTitle: appCopyText(locale)(v, v.Copy().AuthenticationTitle), KeyRef: v.State.KeyRef, AdvancedOpen: open, Identity: template.HTML(identity.String()), ProjectHelp: template.HTML(projectHelp.String()), KeyHelp: template.HTML(keyHelp.String())}, v.AuthConfigRows)
					if err != nil {
						t.Fatal(err)
					}
					if string(actual) != expected.String() {
						t.Fatalf("original form bytes changed\nactual:%s\nexpected:%s", actual, expected.String())
					}
				})
			}
		}
	}
}

func TestAppSettingsMarkupRejectsChangedAuthorityAndControls(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-settings-source-v5.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.SettingsViewSource
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string][2]string{
		"lost-identity":      {"{{.Identity}}", ""},
		"duplicate-identity": {"{{.Identity}}", "{{.Identity}}{{.Identity}}"},
		"lost-rows":          {"{{.Rows}}", ""},
		"duplicate-rows":     {"{{.Rows}}", "{{.Rows}}{{.Rows}}"},
		"lost-required-help": {"{{.KeyHelp}}", ""},
		"external-submit":    {"action=\"/apps/service/configure\"", "action=\"https://example.invalid\""},
		"read-submit":        {"method=\"post\"", "method=\"get\""},
		"credential-input":   {"name=\"key_ref\"", "name=\"api_key\""},
		"extra-input":        {"{{.Identity}}", "{{.Identity}}<input name=\"secret\">"},
		"lost-required":      {"required autocomplete", "autocomplete"},
		"hidden-key":         {"name=\"key_ref\"", "type=\"hidden\" name=\"key_ref\""},
		"changed-editor":     {"name=\"editor\" value=\"fields\"", "name=\"editor\" value=\"json\""},
		"changed-row-id":     {"remove-override-{{.Index}}", "remove-override-0"},
		"changed-row-bound":  {"maxlength=\"128\"", "maxlength=\"129\""},
		"changed-save":       {"type=\"submit\"", "type=\"submit\" name=\"action\" value=\"add-override\""},
		"hidden-action":      {"formnovalidate", "hidden formnovalidate"},
		"script":             {"<summary>", "<script>alert(1)</script><summary>"},
		"event":              {"<fieldset>", "<fieldset onclick=\"evil()\">"},
		"private-state":      {"{{.KeyRef}}", "{{.State.KeyRef}}"},
		"candidate-loop":     {"{{.Rows}}", "{{range .Rows}}{{.}}{{end}}"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(source.Program, change[0], change[1], 1)
			if bad == source.Program {
				t.Fatal("negative did not mutate source")
			}
			if _, err := compileAppSettingsMarkup(bad); err == nil {
				t.Fatal("changed original control accepted")
			}
		})
	}
}

func TestAppSettingsMarkupActualDefaultSourceAndNoFallback(t *testing.T) {
	policies := actualLocalizedAppPolicies(t)
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			policy := policies[locale]
			if !policy.settingsManaged || policy.settingsMarkup == nil {
				t.Fatal("actual plugin did not attach settings source")
			}
			v := &appsView{Interface: &policy, View: connectedapps.View{State: connectedapps.State{KeyRef: "env:ORIGINAL_PROJECT", Revision: 81}}, AuthConfigRows: []appAuthConfigRow{{App: "gmail", Config: "ac_original"}, {App: "raw<&", Config: `draft"&`}}}
			p := page{Apps: v, Notice: "original draft"}
			host := newLayout(locale)
			got, err := appSettingsProject(host, p, locale)
			if err != nil {
				t.Fatal(err)
			}
			var expected bytes.Buffer
			if err := host.ExecuteTemplate(&expected, "app-service-project-legacy-v4", p); err != nil {
				t.Fatal(err)
			}
			if string(got) != expected.String() {
				t.Fatal("actual managed source changed original controls or text")
			}
			policy = UnavailableAppSettingsMarkup(policy)
			if _, err := appSettingsProject(host, p, locale); err == nil {
				t.Fatal("unavailable current contribution restored historical template")
			}
		})
	}
}
