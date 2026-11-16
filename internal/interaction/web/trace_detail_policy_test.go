package web

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTraceOwnedDetailPolicyMatchesOriginalCapabilityAndStepMatrix(t *testing.T) {
	var original strings.Builder
	for _, name := range []string{"managed-traces.html.txt", "managed-observation-detail.html.txt"} {
		raw, err := os.ReadFile("../../../tests/fixtures/plugins/trace-workbench-original-v1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		original.Write(raw)
	}
	negative, zero, one := int64(-1), int64(0), int64(1)
	count := 0
	for _, lang := range []string{"en", "zh-CN"} {
		r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
		if lang == "zh-CN" {
			r.AddCookie(&http.Cookie{Name: languageCookie, Value: lang})
		}
		candidate, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
		if err != nil {
			t.Fatal(err)
		}
		baseline, err := requestLayout(r).Parse(original.String())
		if err != nil {
			t.Fatal(err)
		}
		for _, capabilities := range [][]string{nil, {}, {"overview"}, {"context"}, {"wire"}, {"diff"}, {"context", "wire", "diff"}, {"unknown"}} {
			for _, step := range []*int64{nil, &negative, &zero, &one} {
				for _, tab := range []string{"overview", "metadata", "context", "wire", "diff"} {
					v := traceTemplateFixture(t, "metadata")
					v.Tab = tab
					v.Tree.Selected.Step = step
					v.EvidenceTabs = capabilities
					got, err := renderTraceView(candidate, "observability-page", v)
					if err != nil {
						t.Fatal(lang, capabilities, step, tab, err)
					}
					old := *v
					old.Tabs = legacyTracePrimaryTabsV1(old.Tree.Selected)
					var want bytes.Buffer
					if err = baseline.ExecuteTemplate(&want, "observability-page", page{Trace: &old}); err != nil {
						t.Fatal(err)
					}
					if string(got) != want.String() {
						t.Fatal("original detail visibility/grouping changed", lang, capabilities, step, tab)
					}
					count++
				}
			}
		}
	}
	if count != 320 {
		t.Fatal("detail matrix coverage missing", count)
	}
}
func TestTraceOwnedTaskActionLabelsKeepOriginalUnknownAndLanguageBehavior(t *testing.T) {
	cases := []struct{ operation, label string }{{"create", "Created Task"}, {"append", "Appended instruction"}, {"pause", "Paused Task"}, {"cancel", "Cancelled Task"}, {"complete", "Completed Task"}, {"", "Recorded Task action"}, {"unknown", "Recorded Task action"}, {"Create", "Recorded Task action"}}
	for _, lang := range []string{"en", "zh-CN"} {
		r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
		if lang == "zh-CN" {
			r.AddCookie(&http.Cookie{Name: languageCookie, Value: lang})
		}
		view, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			var out bytes.Buffer
			if err = view.ExecuteTemplate(&out, "trace-task-action", c.operation); err != nil {
				t.Fatal(err)
			}
			if out.String() != template.HTMLEscapeString(translate(lang, c.label)) {
				t.Fatal("task action label changed", lang, c.operation, out.String())
			}
		}
	}
}
func TestTraceManagedDetailCannotCallNativeDisplayDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	for _, name := range []string{"taskAction", "hasPrevious", "traceHasView"} {
		if _, ok := traceViewBindings(r, ownedTraceCopy(t))[name]; ok {
			t.Fatal("native display callback retained", name)
		}
		source := strings.Replace(ownedTraceTemplate(t), `{{traceTr "Tracing"}}`, `{{`+name+`}}`, 1)
		if _, err := compileTraceView(source, r, ownedTraceCopy(t)); err == nil {
			t.Fatal("native display callback callable", name)
		}
	}
	source := strings.Replace(ownedTraceTemplate(t), `{{traceTr "Tracing"}}`, `{{.Trace.Tabs}}`, 1)
	if _, err := compileTraceView(source, r, ownedTraceCopy(t)); err == nil {
		t.Fatal("managed view still exposes native primary tabs")
	}
	for _, kind := range []string{"overview", "metadata"} {
		if v := traceTemplateFixture(t, kind); v.Tabs != nil {
			t.Fatal("managed load still chose native tabs")
		}
	}
}

func TestTracePrimaryTabOrderCanEvolveWithoutHostDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	source := ownedTraceTemplate(t)
	start := strings.Index(source, `<nav class="obs-detail-tabs"`)
	if start < 0 {
		t.Fatal("missing actual primary navigation")
	}
	start += strings.Index(source[start:], ">") + 1
	end := start + strings.Index(source[start:], "</nav>")
	links := source[start:end]
	split := strings.Index(links, "</a>") + len("</a>")
	if split < len("</a>") || split >= len(links) {
		t.Fatal("missing two actual primary links")
	}
	source = source[:start] + links[split:] + links[:split] + source[end:]
	candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	v := traceTemplateFixture(t, "metadata")
	v.Tabs = []string{"unexposed-host-default"}
	out, err := renderTraceView(candidate, "observability-page", v)
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	nav := strings.Index(html, `<nav class="obs-detail-tabs"`)
	if nav < 0 {
		t.Fatal("owning navigation not rendered")
	}
	html = html[nav:]
	html = html[:strings.Index(html, "</nav>")]
	metadata := strings.Index(html, ">Details</a>")
	overview := strings.Index(html, ">Input / Output</a>")
	if metadata < 0 || overview < 0 || metadata >= overview || strings.Contains(html, "unexposed-host-default") {
		t.Fatal("primary grouping still controlled by Host", html)
	}
}
