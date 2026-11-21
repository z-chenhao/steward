package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/v1"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapabilityOwnedViewRetainsOriginalFormSurface(t *testing.T) {
	in := contract.Request{Operation: "prepare", ID: "memory", Text: "encryption_key_ref: env:BOUND\n", WithView: true, Values: []contract.Field{}}
	out := v1.Project(in)
	var copy []contract.Translation
	if err := json.Unmarshal([]byte(out.Copy.Source), &copy); err != nil {
		t.Fatal(err)
	}
	parsed, err := compileCapabilityView(out.View.Source, "en", copy)
	if err != nil {
		t.Fatal("compile", err)
	}
	p := page{View: "capabilities", CapabilityView: &capabilityViewAssets{out.View, out.Copy}, Capability: &Capability{ID: "memory", Title: "Memory", Revision: "bound-revision", Configured: true, Form: out.Form}}
	data := capabilityViewModel(p)
	var body bytes.Buffer
	if err = parsed.ExecuteTemplate(&body, "capabilities-page", data); err != nil {
		t.Fatal("execute", err)
	}
	if !safeCapabilityViewDOM(body.String(), data) {
		t.Fatal("DOM rejected original controlled form", body.String())
	}
	if _, err = renderCapabilityView(p, httptest.NewRequest("GET", "/settings/capabilities/memory", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityOwnedViewRejectsEffectEscapesAndPrivateFields(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"script", "<section class=", "<script>bad()</script><section class="},
		{"noscript-network", "</form>", `<noscript><img src="https://outside.invalid/leak"></noscript></form>`},
		{"alternate-effect", `action="/capability/configure"`, `action="/model/delete"`},
		{"foreign-link", `href="/settings/capabilities"`, `href="https://outside.invalid"`},
		{"wrong-revision", `value="{{.Revision}}"`, `value="foreign-revision"`},
		{"hidden-key", `<input data-required name="key_ref"`, `<input type="hidden" data-required name="key_ref"`},
		{"foreign-field", `name="key_ref"`, `name="private_token"`},
		{"private-data", `{{capabilityTr .Title}}`, `{{.Path}}`},
		{"overridden-atom", `{{define "capability-status"}}`, `{{define "help"}}bad{{end}}{{define "capability-status"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := v1.Project(contract.Request{Operation: "prepare", ID: "memory", Text: "encryption_key_ref: env:BOUND\n", WithView: true, Values: []contract.Field{}})
			source := strings.Replace(out.View.Source, tc.from, tc.to, 1)
			if source == out.View.Source {
				t.Fatal("fixture marker missing")
			}
			out.View.Source = source
			out.View.Digest = rsiplugin.FileDigest([]byte(source))
			p := page{View: "capabilities", CapabilityView: &capabilityViewAssets{out.View, out.Copy}, Capability: &Capability{ID: "memory", Title: "Memory", Revision: "bound-revision", Configured: true, Form: out.Form}}
			if _, err := renderCapabilityView(p, httptest.NewRequest("GET", "/settings/capabilities/memory", nil)); err == nil {
				t.Fatal("candidate escaped Host view boundary")
			}
		})
	}
}
func TestCapabilityOwnedCopyDoesNotUseAnotherVersionLexicon(t *testing.T) {
	out := v1.Project(contract.Request{Operation: "prepare", ID: "memory", WithView: true, Values: []contract.Field{}})
	var entries []contract.Translation
	if err := json.Unmarshal([]byte(out.Copy.Source), &entries); err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if entries[i].Key == "Memory" {
			entries[i].Value = "独立记忆视图"
		}
	}
	compiled, err := compileCapabilityView(out.View.Source, "zh-CN", entries)
	if err != nil {
		t.Fatal(err)
	}
	data := capabilityViewData{Capability: &capabilityViewRecord{ID: "memory", Title: "Memory", Form: &contract.Form{}}}
	var body bytes.Buffer
	if err = compiled.ExecuteTemplate(&body, "capabilities-page", data); err != nil || !strings.Contains(body.String(), "独立记忆视图") {
		t.Fatal(body.String(), err)
	}
	filtered := []contract.Translation{}
	for _, entry := range entries {
		if entry.Key != "Memory" {
			filtered = append(filtered, entry)
		}
	}
	compiled, err = compileCapabilityView(out.View.Source, "zh-CN", filtered)
	if err != nil {
		t.Fatal(err)
	}
	body.Reset()
	if err = compiled.ExecuteTemplate(&body, "capabilities-page", data); err != nil || !strings.Contains(body.String(), `aria-label="Memory"`) {
		t.Fatal("missing owned entry used native dictionary", body.String(), err)
	}
}

func TestCapabilityOwnedViewBoundsEmptyHelperExpansionAndIntegerLoops(t *testing.T) {
	out := v1.Project(contract.Request{Operation: "prepare", ID: "memory", WithView: true, Values: []contract.Field{}})
	var entries []contract.Translation
	if err := json.Unmarshal([]byte(out.Copy.Source), &entries); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{`{{range 999999999999}}empty{{end}}`, `{{range .Capabilities}}{{range .Capabilities}}{{range .Capabilities}}{{range .Capabilities}}{{end}}{{end}}{{end}}{{end}}`} {
		source := strings.Replace(out.View.Source, `{{define "capabilities-page"}}`, `{{define "capabilities-page"}}`+extra, 1)
		if _, err := compileCapabilityView(source, "en", entries); err == nil {
			t.Fatal("unbounded private view accepted")
		}
	}
	helpers := ""
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("capability-helper-%d", i)
		next := fmt.Sprintf("capability-helper-%d", i+1)
		helpers += `{{define "` + name + `"}}{{template "` + next + `" .}}{{template "` + next + `" .}}{{end}}`
	}
	helpers += `{{define "capability-helper-30"}}{{end}}`
	source := strings.Replace(out.View.Source, `{{define "capabilities-page"}}`, `{{define "capabilities-page"}}{{template "capability-helper-0" .}}`, 1) + helpers
	if _, err := compileCapabilityView(source, "en", entries); err == nil {
		t.Fatal("exponential empty helper expansion accepted")
	}
}

func TestCapabilityOwnedCopyLocalizesFormattedDiagnostic(t *testing.T) {
	out := v1.Project(contract.Request{Operation: "prepare", ID: "embedding", WithView: true, Values: []contract.Field{}})
	var entries []contract.Translation
	if err := json.Unmarshal([]byte(out.Copy.Source), &entries); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileCapabilityView(out.View.Source, "zh-CN", entries)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	data := capabilityViewData{Capability: &capabilityViewRecord{ID: "embedding", Title: "Semantic search", State: "unavailable", Form: &contract.Form{}, Message: "Embedding endpoint returned a valid 768-dimensional vector for a fixed diagnostic sentence."}}
	if err = compiled.ExecuteTemplate(&body, "capabilities-page", data); err != nil || !strings.Contains(body.String(), "嵌入端点已为固定诊断句返回有效的 768 维向量。") {
		t.Fatal("owned formatted diagnostic not localized", body.String(), err)
	}
}
