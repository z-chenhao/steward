package web

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/observability"
)

func TestTraceStylesOriginalIndependentLayoutAndPassiveBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../plugin/traceinspector/view/trace-activity.css")
	if err != nil {
		t.Fatal(err)
	}
	if err = validateTraceStylesheet(string(raw)); err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(raw), "height:88px", "height:96px")
	changed = strings.ReplaceAll(changed, "--timeline-width:44%", "--timeline-width:48%")
	changed = strings.ReplaceAll(changed, ".obs-tree-guides>span{width:12px", ".obs-tree-guides>span{width:16px")
	changed = strings.ReplaceAll(changed, ".obs-wire-reader{display:grid;gap:20px}", ".obs-wire-reader{display:grid;gap:24px}")
	if changed == string(raw) {
		t.Fatal("unchanged")
	}
	if err = validateTraceStylesheet(changed); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`.obs-activity{background:url(/private)}`, `@import url(/private);`, `.obs-activity[data-trace-id=private]{padding:8px}`, `body{display:none}`, `.obs-tree-guides>span:last-child::after{content:"private"}`, `.obs-tree-guides>span:last-child::after{content:attr(data-private)}`, `.obs-node-name{display:none!important}`, `.observation-node[data-running=private] .obs-bar{fill:var(--foreground)}`, `.obs-tree-pane{--timeline-width:url(/private)}`, `.obs-node-name{display:none}`, `@container private (max-width:340px){.obs-generation-metrics{gap:12px 8px}}`, `.obs-activity-heading{font-size:0}`, `.obs-activity-chart{height:0}`, `.obs-activity-footer>output{display:none}`, `.obs-activity{padding:8px!important}`, `.obs-activity{content:attr(data-private)}`} {
		if validateTraceStylesheet(bad) == nil {
			t.Fatalf("unsafe style accepted: %s", bad)
		}
	}
}
func TestTraceStylesRoutePinsFrozenResourceAndRejectsUnknownStyle(t *testing.T) {
	ref := "sha256:" + strings.Repeat("a", 64)
	reader := &traceProgramOnlyReader{program: observability.TraceProgram{ReaderRevision: ref, Stylesheet: ".obs-activity-chart{height:96px}"}}
	s := languageServer()
	s.Traces = reader
	for _, q := range []string{"", "reader_revision=", "reader_revision=bad", "reader_revision=" + ref + "&reader_revision=" + ref, "reader_revision=" + ref + "&other=1"} {
		w := httptest.NewRecorder()
		s.traceStylesheet(w, httptest.NewRequest("GET", "http://127.0.0.1/api/developer/traces/style?"+q, nil))
		if w.Code != 400 {
			t.Fatal(w.Code, q)
		}
	}
	w := httptest.NewRecorder()
	s.traceStylesheet(w, httptest.NewRequest("GET", "http://127.0.0.1/api/developer/traces/style?reader_revision="+ref, nil))
	if w.Code != 200 || w.Body.String() != reader.program.Stylesheet || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	reader.program.Stylesheet = `body{display:none}`
	w = httptest.NewRecorder()
	s.traceStylesheet(w, httptest.NewRequest("GET", "http://127.0.0.1/api/developer/traces/style?reader_revision="+ref, nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
