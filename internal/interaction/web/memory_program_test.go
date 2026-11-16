package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/memory"
)

type memoryProgramOnlyReader struct {
	memoryStyleOnlyReader
	revision, program string
}

func (r *memoryProgramOnlyReader) MemoryInspectorProgram(_ context.Context, ref string) (memory.InspectionProgram, error) {
	r.calls++
	if ref != "sha256:original" {
		return memory.InspectionProgram{}, memory.ErrInspectionUnavailable
	}
	return memory.InspectionProgram{ReaderRevision: r.revision, Source: r.program}, nil
}

func TestMemoryProgramRouteFrozenIsolationAndNoFactReads(t *testing.T) {
	reader := &memoryProgramOnlyReader{revision: "sha256:original", program: "self.onmessage=()=>postMessage('</script><img src=x>')"}
	s := languageServer()
	s.Memory = reader
	path := "/api/developer/memory/program?inspector_revision=sha256%3Aoriginal"
	request := func(method, path, dest string, authorized bool) *httptest.ResponseRecorder {
		r := languageRequest(method, path, "en", "")
		r.Header.Set("Sec-Fetch-Dest", dest)
		if !authorized {
			r.Header.Del("Authorization")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", path, "iframe", false); w.Code != http.StatusUnauthorized || reader.calls != 0 {
		t.Fatal("unowned program read", w.Code, reader.calls)
	}
	for _, q := range []string{"", "?inspector_revision=", "?inspector_revision=a&inspector_revision=b", "?inspector_revision=sha256%3Aoriginal&sql=select", "?inspector_revision=%zz"} {
		if w := request("GET", "/api/developer/memory/program"+q, "iframe", true); w.Code != http.StatusBadRequest || reader.calls != 0 {
			t.Fatal("invalid query reached reader", q, w.Code)
		}
	}
	w := request("GET", path, "iframe", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "steward-memory-browser") || strings.Contains(w.Body.String(), "</script><img") {
		t.Fatal("unsafe program relay", w.Code)
	}
	for _, rule := range []string{"sandbox allow-scripts;", "connect-src 'none'", "worker-src blob:", "frame-ancestors 'self'", "form-action 'none'"} {
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), rule) {
			t.Fatal("missing isolation", rule)
		}
	}
	if strings.Contains(w.Header().Get("Content-Security-Policy"), "allow-same-origin") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("lost opaque isolation")
	}
	if w = request("GET", path, "document", true); w.Code != http.StatusOK || w.Body.String() != "Memory isolated presentation program" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatal("top navigation executed program", w.Code)
	}
	calls := reader.calls
	if w = request("POST", path, "iframe", true); w.Code != http.StatusNotFound || reader.calls != calls {
		t.Fatal("write reached program", w.Code)
	}
	for _, source := range []string{"", strings.Repeat("x", (64<<10)+1), string([]byte{0xff})} {
		reader.program = source
		if w = request("GET", path, "iframe", true); w.Code != http.StatusServiceUnavailable {
			t.Fatal("invalid program released", w.Code)
		}
	}
	reader.program = "self.onmessage=()=>{}"
	reader.revision = "sha256:other"
	if w = request("GET", path, "iframe", true); w.Code != http.StatusServiceUnavailable {
		t.Fatal("version drift released", w.Code)
	}
	if w = request("GET", "/api/developer/memory/program?inspector_revision=sha256%3Amissing", "iframe", true); w.Code != http.StatusServiceUnavailable {
		t.Fatal("missing program fell back", w.Code)
	}
	if reader.factReads != 0 {
		t.Fatal("program asset loaded private facts", reader.factReads)
	}
}
