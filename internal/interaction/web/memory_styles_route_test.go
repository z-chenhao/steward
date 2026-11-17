package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/memory"
)

type memoryStyleOnlyReader struct {
	source           string
	calls, factReads int
}

func (r *memoryStyleOnlyReader) InspectMemory(context.Context, memory.InspectionQuery) (memory.InspectionPage, error) {
	r.factReads++
	return memory.InspectionPage{}, memory.ErrInspectionUnavailable
}
func (r *memoryStyleOnlyReader) MemoryInspectorStylesheet(_ context.Context, ref string) (memory.InspectionStylesheet, error) {
	r.calls++
	if ref != "sha256:original" {
		return memory.InspectionStylesheet{}, memory.ErrInspectionUnavailable
	}
	return memory.InspectionStylesheet{ReaderRevision: ref, Source: r.source}, nil
}

func TestMemoryStylesRouteUsesOnlyFrozenAssetReaderAndOriginalOwnerBoundary(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/memory.css.txt")
	if e != nil {
		t.Fatal(e)
	}
	reader := &memoryStyleOnlyReader{source: string(raw)}
	s := languageServer()
	s.Memory = reader
	path := "/api/developer/memory/style?inspector_revision=sha256%3Aoriginal"
	request := func(method, path string, authorized bool) *httptest.ResponseRecorder {
		r := languageRequest(method, path, "en", "")
		if !authorized {
			r.Header.Del("Authorization")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", path, false); w.Code != http.StatusUnauthorized || reader.calls != 0 {
		t.Fatal("unowned asset access reached reader", w.Code, reader.calls)
	}
	for _, query := range []string{"", "?inspector_revision=", "?inspector_revision=one&inspector_revision=two", "?inspector_revision=sha256%3Aoriginal&sql=select", "?inspector_revision=sha256%3Aoriginal&bad=%zz"} {
		if w := request("GET", "/api/developer/memory/style"+query, true); w.Code != http.StatusBadRequest || reader.calls != 0 {
			t.Fatal("invalid asset query reached reader", query, w.Code, reader.calls)
		}
	}
	w := request("GET", path, true)
	if w.Code != http.StatusOK || w.Body.String() != string(raw) || w.Header().Get("Content-Type") != "text/css; charset=utf-8" || w.Header().Get("Cache-Control") != "private, no-store" || reader.calls != 1 {
		t.Fatal("frozen stylesheet mismatch", w.Code, reader.calls, w.Header())
	}
	if w = request("POST", path, true); w.Code != http.StatusNotFound || reader.calls != 1 {
		t.Fatal("asset route accepted write", w.Code, reader.calls)
	}
	if w = request("GET", "/api/developer/memory/style?inspector_revision=sha256%3Amissing", true); w.Code != http.StatusServiceUnavailable {
		t.Fatal("missing managed asset fell back", w.Code)
	}
	reader.source = ".memory-content{display:none}"
	if w = request("GET", path, true); w.Code != http.StatusServiceUnavailable {
		t.Fatal("unsafe owning stylesheet released", w.Code)
	}
	if reader.factReads != 0 {
		t.Fatal("asset route loaded Memory facts", reader.factReads)
	}
}
