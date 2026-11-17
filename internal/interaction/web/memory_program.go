package web

import (
	"context"
	"net/http"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/memory"
)

type MemoryProgramReader interface {
	MemoryInspectorProgram(context.Context, string) (memory.InspectionProgram, error)
}

func (s *Server) memoryProgram(w http.ResponseWriter, r *http.Request) {
	reference, valid := memoryAssetReference(r)
	if !valid {
		fail(w, http.StatusBadRequest)
		return
	}
	reader, ok := s.Memory.(MemoryProgramReader)
	if !ok {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	p, err := reader.MemoryInspectorProgram(r.Context(), reference)
	if err != nil || p.ReaderRevision != reference || len(p.Source) == 0 || len(p.Source) > 64<<10 || !utf8.ValidString(p.Source) {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	renderIsolatedBrowser(w, r, p.Source, "steward-memory-browser", "Memory", 64<<10)
}
