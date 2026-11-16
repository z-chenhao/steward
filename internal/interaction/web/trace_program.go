package web

import (
	"context"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/observability"
)

type TraceProgramReader interface {
	TraceWorkbenchProgram(context.Context, string) (observability.TraceProgram, error)
}

func (s *Server) traceProgram(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["reader_revision"]) != 1 || q.Get("reader_revision") == "" || !observability.ValidReaderRevision(q.Get("reader_revision")) {
		fail(w, http.StatusBadRequest)
		return
	}
	reference := q.Get("reader_revision")
	reader, ok := s.Traces.(TraceProgramReader)
	if !ok {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	program, err := reader.TraceWorkbenchProgram(r.Context(), reference)
	if err != nil || program.ReaderRevision != reference || len(program.Source) == 0 || len(program.Source) > 64<<10 || !utf8.ValidString(program.Source) {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	renderIsolatedBrowser(w, r, program.Source, "steward-trace-browser", "Trace", 64<<10)
}
