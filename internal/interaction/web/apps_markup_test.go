package web

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"os"
	"strings"
	"testing"
)

func TestAppsMarkupPassiveSourceAndMeaningfulVariant(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-markup-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.MarkupSource
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if _, err := compileAppMarkup(source, appCardProgramLegacyV1(), appSidebarProgramLegacyV1()); err != nil {
		t.Fatal("native source rejected", err)
	}
	source.Card = strings.Replace(source.Card, `<span class="app-copy">`, `<div class="app-copy" data-plugin-copy="v1.6">`, 1)
	source.Card = strings.Replace(source.Card, `"app-card-atom-end-copy"}}</span>`, `"app-card-atom-end-copy"}}</div>`, 1)
	if _, err := compileAppMarkup(source, appCardProgramLegacyV1(), appSidebarProgramLegacyV1()); err != nil {
		t.Fatal("real markup variant rejected", err)
	}
}

func TestAppsMarkupRejectsReflectionExecutionAndChangedBindings(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-markup-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var original contract.MarkupSource
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, old, new string
		sidebar        bool
	}{
		{"domain-reflection", "{{.Name}}", "{{.Apps.State.KeyRef}}", false},
		{"method-chain", "{{.Name}}", "{{.Name.String}}", false},
		{"function", "{{.Name}}", "{{printf \"%s\" .Name}}", false},
		{"variable", "{{.Name}}", "{{$name := .Name}}{{$name}}", false},
		{"range", "{{.Name}}", "{{range .Name}}{{.}}{{end}}", false},
		{"with", "{{.Name}}", "{{with .Name}}{{.}}{{end}}", false},
		{"include", "{{.Name}}", "{{template \"atoms\" .}}", false},
		{"string-branch", "{{.Name}}", "{{if .Name}}{{.Name}}{{end}}", false},
		{"javascript", "<strong>", "<script>alert(1)</script><strong>", false},
		{"event", "<strong>", "<strong onclick=\"alert(1)\">", false},
		{"inline-css", "<strong>", "<strong style=\"display:none\">", false},
		{"global-id", "<strong>", "<strong id=\"composio-key-ref\">", false},
		{"foreign-url", `href="{{.URL}}"`, `href="https://example.invalid/steal?name={{.Name}}"`, false},
		{"data-hook", `<strong>`, `<strong data-app-action>`, false},
		{"hidden-name", `<strong>`, `<strong hidden>`, false},
		{"hidden-control-class", `<strong>`, `<strong class="control-icon-button">`, false},
		{"wrong-intent", `name="action" value="connect"`, `name="action" value="disconnect"`, false},
		{"wrong-toolkit", `name="toolkit" value="{{.Slug}}"`, `name="toolkit" value="other_app"`, false},
		{"missing-reference", `{{.InterfaceReference}}`, "", false},
		{"missing-icon", `{{.Icon}}`, "", false},
		{"field-in-data", `<strong>`, `<strong data-plugin-value="{{.Name}}">`, false},
		{"iframe", `<strong>`, `<iframe src="/settings/apps"></iframe><strong>`, false},
		{"sidebar-private", `{{.Name}}`, `{{.State.Connections}}`, true},
		{"sidebar-wrong-target", `href="{{.URL}}"`, `href="/settings/models"`, true},
		{"sidebar-missing-pending", `{{if .Pending}}`, `{{if .Pending}}<!--`, true},
		{"sidebar-fake-pending", `aria-label="{{.PendingLabel}}"`, `aria-label="Connected"`, true},
		{"slot-in-attribute", `{{.Icon}}`, `<span title="{{.Icon}}"></span>`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := original
			target := &source.Card
			if c.sidebar {
				target = &source.Sidebar
			}
			if !strings.Contains(*target, c.old) {
				t.Fatal("negative marker absent")
			}
			*target = strings.Replace(*target, c.old, c.new, 1)
			if _, err := compileAppMarkup(source, appCardProgramLegacyV1(), appSidebarProgramLegacyV1()); err == nil {
				t.Fatal("unsafe source accepted", c.name)
			}
		})
	}
}
