package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/observability"
)

type traceProgramOnlyReader struct {
	traceReader
	program observability.TraceProgram
	calls   int
	err     error
}

func (r *traceProgramOnlyReader) TraceWorkbenchProgram(_ context.Context, _ string) (observability.TraceProgram, error) {
	r.calls++
	return r.program, r.err
}
func TestTraceProgramRequiresExactFrozenReferenceAndOpaqueWorker(t *testing.T) {
	ref := "sha256:" + strings.Repeat("a", 64)
	reader := &traceProgramOnlyReader{program: observability.TraceProgram{ReaderRevision: ref, Source: "self.onmessage=()=>postMessage('</script><img src=x>')"}}
	s := languageServer()
	s.Traces = reader
	request := func(path, destination string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		if auth {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		r.Header.Set("Sec-Fetch-Dest", destination)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	path := "/api/developer/traces/program?reader_revision=" + ref
	if w := request(path, "iframe", false); w.Code != http.StatusUnauthorized || reader.calls != 0 {
		t.Fatal("unauthorized program access", w.Code)
	}
	for _, query := range []string{"", "?reader_revision=", "?reader_revision=sha256:invalid", "?reader_revision=" + ref + "&reader_revision=" + ref, "?reader_revision=" + ref + "&run=private", "?reader_revision=%zz"} {
		if w := request("/api/developer/traces/program"+query, "iframe", true); w.Code != http.StatusBadRequest || reader.calls != 0 {
			t.Fatal("ambiguous resource request accepted", query, w.Code)
		}
	}
	w := request(path, "iframe", true)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, required := range []string{"sandbox allow-scripts", "connect-src 'none'", "frame-src 'none'", "form-action 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), required) {
			t.Fatal("missing isolation", required)
		}
	}
	if strings.Contains(w.Body.String(), "</script><img") || !strings.Contains(w.Body.String(), "new Worker") || !strings.Contains(w.Body.String(), "steward-trace-browser") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("unsafe program relay")
	}
	if w = request(path, "document", true); w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Body.String() != "Trace isolated presentation program" {
		t.Fatal("top navigation executed program")
	}
	reader.program.ReaderRevision = "sha256:" + strings.Repeat("b", 64)
	if request(path, "iframe", true).Code != 503 {
		t.Fatal("wrong graph accepted")
	}
	reader.program.ReaderRevision = ref
	reader.err = errors.New("private-marker")
	w = request(path, "iframe", true)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-marker") {
		t.Fatal("failure restored program or disclosed private error")
	}
	s.Traces = traceReader{}
	if request(path, "iframe", true).Code != 503 {
		t.Fatal("native fallback accepted")
	}
}
