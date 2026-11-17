package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/z-chenhao/assistant/internal/memory"
)

type MemoryStylesheetReader interface {
	MemoryInspectorStylesheet(context.Context, string) (memory.InspectionStylesheet, error)
}

func (s *Server) memoryStylesheet(w http.ResponseWriter, r *http.Request) {
	reference, valid := memoryAssetReference(r)
	if !valid {
		fail(w, http.StatusBadRequest)
		return
	}
	reader, ok := s.Memory.(MemoryStylesheetReader)
	if !ok {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	styles, err := reader.MemoryInspectorStylesheet(r.Context(), reference)
	if err != nil || styles.ReaderRevision != reference || validateMemoryStylesheet(styles.Source) != nil {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = fmt.Fprint(w, styles.Source)
}

func memoryAssetReference(r *http.Request) (string, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["inspector_revision"]) != 1 || q.Get("inspector_revision") == "" || len(q.Get("inspector_revision")) > 256 {
		return "", false
	}
	return q.Get("inspector_revision"), true
}
