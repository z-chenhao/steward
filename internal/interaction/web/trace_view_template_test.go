package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func ownedTraceTemplate(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../plugin/traceinspector/view/trace.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func traceTemplateFixture(t *testing.T, kind string) *traceView {
	t.Helper()
	reader := &selectedTraceWorkbenchReader{revision: traceVersionRef("a")}
	s := languageServer()
	s.Traces = reader
	s.Inspector = &inspectionReader{}
	v := &traceView{Tab: "overview"}
	if kind != "list" {
		v.Run = "root-public"
		v.Node = "generation:g"
	}
	if kind == "metadata" || kind == "wire" {
		v.Tab = kind
	}
	if err := s.loadTrace(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestTraceViewOwnedTemplatesMatchAcceptedOriginalReadModels(t *testing.T) {
	source := ownedTraceTemplate(t)
	var original strings.Builder
	for _, file := range []string{"managed-traces.html.txt", "managed-observation-detail.html.txt"} {
		raw, err := os.ReadFile("../../../tests/fixtures/plugins/trace-workbench-original-v1/" + file)
		if err != nil {
			t.Fatal(err)
		}
		original.Write(raw)
	}
	for _, kind := range []string{"list", "overview", "metadata", "wire"} {
		for _, language := range []string{"en", "zh-CN"} {
			t.Run(kind+"/"+language, func(t *testing.T) {
				v := traceTemplateFixture(t, kind)
				r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
				if language == "zh-CN" {
					r.AddCookie(&http.Cookie{Name: languageCookie, Value: language})
				}
				candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
				if err != nil {
					t.Fatal("compile", err)
				}
				baseline, err := requestLayout(r).Parse(original.String())
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"observability-page", "trace-list-time-controls"} {
					got, err := renderTraceView(candidate, name, v)
					if err != nil {
						t.Fatal("accept", name, err)
					}
					var want bytes.Buffer
					originalView := *v
					if originalView.Tree != nil {
						originalView.Tabs = legacyTracePrimaryTabsV1(originalView.Tree.Selected)
					}
					if err = baseline.ExecuteTemplate(&want, name, page{Trace: &originalView}); err != nil {
						t.Fatal(err)
					}
					if string(got) != want.String() {
						t.Fatal("changed accepted original DOM", name)
					}
				}
			})
		}
	}
}
func TestTraceViewRejectsEffectsForeignOwnersAndUnboundedSyntax(t *testing.T) {
	source := ownedTraceTemplate(t)
	v := traceTemplateFixture(t, "list")
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	for name, change := range map[string][2]string{
		"script":              {"<section class=", "<script>fetch('/private')</script><section class="},
		"foreign-link":        {"<section class=", "<a href=\"https://outside.invalid/?body={{.Trace.List.Items}}\">outside</a><section class="},
		"effect-attribute":    {"<section class=", "<section data-api-delete=\"private\" class="},
		"effect-form":         {"method=\"get\"", "method=\"post\""},
		"effect-route":        {"action=\"/developer/observability\"", "action=\"/api/tasks/delete\""},
		"event-handler":       {"<section class=", "<section onclick=\"fetch('/private')\" class="},
		"external-svg-paint":  {"<section class=", `<svg fill="url(https://outside.invalid/paint.svg)"><path d="M0 0"/></svg><section class=`},
		"image-effect":        {"<section class=", "<img src=\"/private/delete\"><section class="},
		"style":               {"<section class=", "<section style=\"display:none\" class="},
		"foreign-form-owner":  {`form="trace-filters"`, `form="conversation-form"`},
		"foreign-dom-id":      {`id="trace-filters"`, `id="conversation-form"`},
		"hidden-row":          {`<tr data-execution-id=`, `<tr hidden data-execution-id=`},
		"foreign-controller":  {"<section class=", "<section class=\"conversation-send\"></section><section class="},
		"foreign-owner":       {"{{with .Trace}}", "{{with .Memory}}"},
		"legacy-tab-model":    {"{{with .Trace}}", "{{with .Trace.Tabs}}"},
		"source-resource":     {"{{with .Trace}}", "{{with .Trace.ViewSource}}"},
		"host-method":         {"{{with .Trace}}", "{{with .Trace.StylesheetURL}}"},
		"shared-redefinition": {"{{define \"trace-cost\"}}", "{{define \"identifier\"}}"},
		"recursion":           {"{{define \"trace-cost\"}}", "{{define \"trace-cost\"}}{{template \"trace-cost\" .}}"},
		"foreign-template":    {"{{with .Trace}}", "{{template \"settings\" .}}{{with .Trace}}"},
		"unsafe-builtin":      {"{{with .Trace}}", "{{html \"<script>\"}}{{with .Trace}}"},
		"different-reader":    {"data-trace-reader-revision=\"{{.Trace.ReaderRevision}}\"", "data-trace-reader-revision=\"sha256:foreign\""},
		"hidden-root":         {"<section class=", "<section hidden class="},
		"missing-row":         {"data-execution-id=\"{{.ID}}\"", "data-execution-id=\"forged\""},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(source, change[0]) {
				t.Fatal("test did not alter source")
			}
			altered := strings.Replace(source, change[0], change[1], 1)
			view, err := compileTraceView(altered, r, ownedTraceCopy(t))
			if err == nil {
				for _, part := range []string{"observability-page", "trace-list-time-controls"} {
					_, err = renderTraceView(view, part, v)
					if err != nil {
						break
					}
				}
			}
			if err == nil {
				t.Fatal("candidate accepted forbidden behavior")
			}
		})
	}
	if _, err := compileTraceView(strings.Repeat(" ", 65537), r, ownedTraceCopy(t)); err == nil {
		t.Fatal("unbounded source accepted")
	}
}
func TestTraceManagedPageActuallyUsesOwningTemplateAndFailsClosed(t *testing.T) {
	v := traceTemplateFixture(t, "list")
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	v.ViewSource = strings.Replace(v.ViewSource, `{{traceTr "Tracing"}}`, `{{traceTr "Versioned trace directory"}}`, 1)
	s := languageServer()
	w := httptest.NewRecorder()
	s.render(w, r, page{Trace: v, View: "traces", Tab: "/developer", Title: "Traces"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Versioned trace directory") || strings.Contains(w.Body.String(), ">Tracing</h1>") {
		t.Fatal("managed template not dispatched", w.Code)
	}
	v.ViewSource = ""
	w = httptest.NewRecorder()
	s.render(w, r, page{Trace: v, View: "traces"})
	if w.Code != 503 || strings.Contains(w.Body.String(), "observability-page") {
		t.Fatal("missing template fell back", w.Code)
	}
	v.ViewSource = ownedTraceTemplate(t)
	v.ReaderRevision = ""
	w = httptest.NewRecorder()
	s.render(w, r, page{Trace: v, View: "traces"})
	if w.Code != 503 {
		t.Fatal("unfrozen template accepted")
	}
}

func TestTraceViewCannotReplaceOrOmitSavedRawEvidence(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	for _, kind := range []string{"overview", "wire"} {
		for _, omit := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/replace", true: "/omit"}[omit], func(t *testing.T) {
				v := traceTemplateFixture(t, kind)
				source := ownedTraceTemplate(t)
				if omit {
					source = strings.ReplaceAll(source, ` data-json-source="input"`, "")
					source = strings.ReplaceAll(source, ` data-wire-source`, "")
				} else {
					source = strings.ReplaceAll(source, `{{prettyBody .Content}}`, "forged saved evidence")
				}
				candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
				if err == nil {
					_, err = renderTraceView(candidate, "observability-page", v)
				}
				if err == nil {
					t.Fatal("saved evidence was replaced or omitted")
				}
			})
		}
	}
	source := strings.Replace(ownedTraceTemplate(t), `<section class=`, `{{printf "%999999999s %s" "a" "b"}}<section class=`, 1)
	candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
	if err == nil {
		_, err = renderTraceView(candidate, "observability-page", traceTemplateFixture(t, "list"))
	}
	if err == nil {
		t.Fatal("unbounded formatter accepted")
	}
}
