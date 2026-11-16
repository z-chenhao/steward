package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"html/template"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/observability"
)

type traceHelperOriginalCases struct {
	Kinds []struct {
		Input  observability.Observation `json:"input"`
		Output string                    `json:"output"`
	} `json:"kinds"`
	Geometry []struct {
		Buckets                  []observability.TraceBucket `json:"buckets"`
		Count                    int64                       `json:"count"`
		Height, Offset, X, Width float64
	} `json:"geometry"`

	Columns  []string                                 `json:"columns"`
	Statuses []string                                 `json:"statuses"`
	Labels   []struct{ Family, Input, Output string } `json:"labels"`
	Titles   []struct {
		Input  observability.Trace `json:"input"`
		Output string              `json:"output"`
	} `json:"titles"`
}

func traceOriginalHelperCases(t *testing.T) traceHelperOriginalCases {
	t.Helper()
	const directory = "../../../tests/fixtures/plugins/trace-workbench-original-v1/"
	raw, err := os.ReadFile(directory + "template-helpers-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := os.ReadFile(directory + "template-helpers-provenance-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		ResultsSHA256 string `json:"results_sha256"`
		Files         map[string]struct{ SHA256, Copy string }
	}
	if err = json.Unmarshal(provenance, &proof); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != proof.ResultsSHA256 {
		t.Fatal("original executed cases changed")
	}
	for _, file := range proof.Files {
		raw, err := os.ReadFile(directory + file.Copy)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != file.SHA256 {
			t.Fatal("original source evidence changed", file.Copy, err)
		}
	}
	var cases traceHelperOriginalCases
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}
func TestTraceOwnedLabelsMatchExecutedOriginalNativeCases(t *testing.T) {
	cases := traceOriginalHelperCases(t)
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	candidate, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, c := range cases.Labels {
		name := map[string]string{"providerTimingLabel": "trace-provider-timing-label", "runRole": "trace-role-label", "tabLabel": "trace-tab-label", "observationRole": "trace-observation-role", "observationParent": "trace-parent-run"}[c.Family]
		if name == "" {
			continue
		}
		var out bytes.Buffer
		if err = candidate.ExecuteTemplate(&out, name, c.Input); err != nil {
			t.Fatal(err)
		}
		if out.String() != template.HTMLEscapeString(c.Output) {
			t.Fatalf("%s(%q): got %q want %q", c.Family, c.Input, out.String(), c.Output)
		}
		count++
	}
	for _, c := range cases.Titles {
		var out bytes.Buffer
		if err = candidate.ExecuteTemplate(&out, "trace-title-label", c.Input); err != nil {
			t.Fatal(err)
		}
		if out.String() != template.HTMLEscapeString(c.Output) {
			t.Fatalf("title changed: %+v got %q want %q", c.Input, out.String(), c.Output)
		}
		count++
	}
	if count != 66 {
		t.Fatal("original label/title coverage missing", count)
	}
}
func TestTraceOwnedOptionsAndMainTitlesKeepOriginalFullDOM(t *testing.T) {
	cases := traceOriginalHelperCases(t)
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	candidate, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	var original strings.Builder
	for _, name := range []string{"managed-traces.html.txt", "managed-observation-detail.html.txt"} {
		raw, err := os.ReadFile("../../../tests/fixtures/plugins/trace-workbench-original-v1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		original.Write(raw)
	}
	baseline, err := requestLayout(r).Parse(original.String())
	if err != nil {
		t.Fatal(err)
	}
	v := traceTemplateFixture(t, "list")
	v.List.Items = nil
	for i, c := range cases.Titles {
		item := c.Input
		item.ID = fmt.Sprintf("fixture-%d", i)
		v.List.Items = append(v.List.Items, item)
	}
	v.List.Total = int64(len(v.List.Items))
	for _, status := range append(append([]string{""}, cases.Statuses...), "unknown") {
		v.Query.Status = status
		got, err := renderTraceView(candidate, "observability-page", v)
		if err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		if err = baseline.ExecuteTemplate(&want, "observability-page", page{Trace: v}); err != nil {
			t.Fatal(err)
		}
		if string(got) != want.String() {
			t.Fatal("owning options/title policy changed original full DOM", status)
		}
	}
	for i, name := range cases.Columns {
		needle := fmt.Sprintf(`aria-label="Show column: %s" data-trace-column="%d"`, name, i)
		out, err := renderTraceView(candidate, "observability-page", v)
		if err != nil || !strings.Contains(string(out), needle) {
			t.Fatal("original selectable column lost", name, err)
		}
	}
}
func TestTraceOwnedLabelsCanEvolveWithoutNativePolicyAndKeepMarkupBoundary(t *testing.T) {
	source := ownedTraceTemplate(t)
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	v := traceTemplateFixture(t, "list")
	source = strings.Replace(source, `{{else if eq . "root"}}{{traceTr "Root Worker"}}`, `{{else if eq . "root"}}{{traceTr "Worker v2"}}`, 1)
	candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderTraceView(candidate, "observability-page", v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `title="Worker v2">Worker v2</span>`) {
		t.Fatal("role label still native")
	}
	for _, name := range []string{"traceColumns", "obsStatuses", "providerTimingLabel", "runRole", "tabLabel", "traceLabel", "obsKind", "obsRole", "parentRun", "activityHeight", "activityOffset", "activityX", "activityWidth"} {
		functions := traceViewBindings(r, ownedTraceCopy(t))
		if _, ok := functions[name]; ok {
			t.Fatal("managed native policy binding retained", name)
		}
		altered := strings.Replace(ownedTraceTemplate(t), `{{traceTr "Tracing"}}`, `{{`+name+`}}`, 1)
		if _, err = compileTraceView(altered, r, ownedTraceCopy(t)); err == nil {
			t.Fatal("managed source can call historical policy", name)
		}
	}
}

func TestTraceOwnedNodeKindsAndIconsMatchExecutedOriginalCases(t *testing.T) {
	cases := traceOriginalHelperCases(t)
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	candidate, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Kinds {
		var out bytes.Buffer
		if err = candidate.ExecuteTemplate(&out, "trace-observation-kind", c.Input); err != nil {
			t.Fatal(err)
		}
		if out.String() != template.HTMLEscapeString(c.Output) {
			t.Fatal("source kind changed", c.Input, out.String(), c.Output)
		}
		out.Reset()
		if err = candidate.ExecuteTemplate(&out, "observation-type-icon", c.Input); err != nil {
			t.Fatal(err)
		}
		var original bytes.Buffer
		if err = requestLayout(r).ExecuteTemplate(&original, "observation-type-icon", c.Output); err != nil {
			t.Fatal(err)
		}
		if out.String() != original.String() {
			t.Fatal("original source kind/icon precedence changed", c.Input)
		}
	}
	if len(cases.Kinds) != 72 {
		t.Fatal("missing original kind cases")
	}
}

func traceRenderedActivityRects(t *testing.T, source string) []map[string]float64 {
	t.Helper()
	nodes, err := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]float64{}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, chart bool) {
		for _, a := range n.Attr {
			if a.Key == "class" && strings.Contains(" "+a.Val+" ", " obs-activity-bars ") {
				chart = true
			}
		}
		if chart && n.Type == html.ElementNode && n.Data == "rect" {
			values := map[string]float64{}
			for _, a := range n.Attr {
				if a.Key == "x" || a.Key == "y" || a.Key == "height" || a.Key == "width" {
					v, e := strconv.ParseFloat(a.Val, 64)
					if e != nil {
						t.Fatal("non-numeric SSR geometry", a.Val)
					}
					values[a.Key] = v
				}
			}
			out = append(out, values)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, chart)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}
	return out
}
func TestTraceOwnedSSRGeometryMatchesExecutedOriginalCases(t *testing.T) {
	cases := traceOriginalHelperCases(t)
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	candidate, err := compileTraceView(ownedTraceTemplate(t), r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Geometry {
		v := traceTemplateFixture(t, "list")
		v.List.Activity = c.Buckets
		out, err := renderTraceView(candidate, "observability-page", v)
		if err != nil {
			t.Fatal(err)
		}
		rects := traceRenderedActivityRects(t, string(out))
		if len(rects) != len(c.Buckets) {
			t.Fatal("bucket geometry missing")
		}
		found := false
		for _, rect := range rects {
			if rect["x"] == c.X && rect["y"] == c.Offset && rect["width"] == c.Width && rect["height"] == c.Height {
				found = true
			}
		}
		if !found {
			t.Fatal("original count/position geometry changed", c, rects)
		}
	}
	if len(cases.Geometry) != 9 {
		t.Fatal("missing original SSR cases")
	}
}
func TestTraceOwnedGeometryCanEvolveAndScalarFailuresCloseBeforeRendering(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	v := traceTemplateFixture(t, "list")
	v.List = browserActivityFacts(v.List)
	source := strings.ReplaceAll(ownedTraceTemplate(t), `fraction .Count $activityMax 100`, `fraction .Count $activityMax 90`)
	candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderTraceView(candidate, "observability-page", v)
	if err != nil {
		t.Fatal(err)
	}
	rects := traceRenderedActivityRects(t, string(out))
	if len(rects) != 3 || rects[0]["height"] != 90 || rects[0]["y"] != 10 {
		t.Fatal("candidate geometry still native", rects)
	}
	for _, expression := range []string{`{{fraction 1 0 100}}`, `{{fraction 9223372036854775807 1 1e308}}`, `{{intSub 9223372036854775807 -1}}`, `{{floatSub 1e308 -1e308}}`} {
		altered := strings.Replace(ownedTraceTemplate(t), `<section class=`, expression+`<section class=`, 1)
		view, err := compileTraceView(altered, r, ownedTraceCopy(t))
		if err == nil {
			_, err = renderTraceView(view, "observability-page", v)
		}
		if err == nil {
			t.Fatal("invalid arithmetic rendered", expression)
		}
	}
	// Shared compiler changes preserve Memory's original no-assignment boundary.
	raw, err := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if err != nil {
		t.Fatal(err)
	}
	memorySource := strings.Replace(string(raw), `{{define "memory-page"}}`, `{{define "memory-page"}}{{$ = .Memory}}`, 1)
	if memorySource == string(raw) {
		t.Fatal("memory guard fixture did not change")
	}
	memoryView := originalMemoryViewFixture(t, "document-list")
	if _, err = compileMemoryView(memorySource, memoryViewBindings(memoryView, r), requestLayout(r)); err == nil {
		t.Fatal("Trace assignments widened Memory syntax")
	}
}

func TestTracePrivateTemplateStructureCanEvolveWithoutHostRegistry(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	source := ownedTraceTemplate(t)
	v := traceTemplateFixture(t, "list")
	source = strings.ReplaceAll(source, "trace-role-label", "trace-private-role-refactor")
	candidate, err := compileTraceView(source, r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal("private rename required Host update", err)
	}
	original, err := renderTraceView(candidate, "observability-page", v)
	if err != nil {
		t.Fatal(err)
	}
	extended := source + `{{define "trace-private-unused"}}private implementation{{end}}`
	candidate, err = compileTraceView(extended, r, ownedTraceCopy(t))
	if err != nil {
		t.Fatal("private addition rejected", err)
	}
	out, err := renderTraceView(candidate, "observability-page", v)
	if err != nil || out != original {
		t.Fatal("private addition changed entry behavior", err)
	}
	if _, err = compileTraceView(source, r, ownedTraceCopy(t)); err != nil {
		t.Fatal("private deletion rejected", err)
	}
	for name, altered := range map[string]string{
		"missing-entry":     strings.Replace(source, `{{define "observability-page"}}`, `{{define "trace-private-page"}}`, 1),
		"undefined-private": strings.Replace(source, `{{traceTr "Tracing"}}`, `{{template "trace-private-never-defined" .}}`, 1),
		"foreign-name":      source + `{{define "memory-page"}}foreign owner{{end}}`,
		"shared-override":   source + `{{define "identifier"}}fake identifier{{end}}`,
		"private-cycle":     source + `{{define "trace-private-cycle"}}{{template "trace-private-cycle" .}}{{end}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err = compileTraceView(altered, r, ownedTraceCopy(t)); err == nil {
				t.Fatal("invalid private structure accepted")
			}
		})
	}
}
