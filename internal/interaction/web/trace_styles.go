package web

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/z-chenhao/assistant/internal/observability"
)

var traceStyleCondition = regexp.MustCompile(`^@(?:media|container\s+(?:observation-detail|inspection-layout))\s*\(\s*(?:min|max)-width\s*:\s*([0-9]{1,4})px\s*\)$`)

func validateTraceStylesheet(source string) error {
	// Only the original empty guide content and three badge declarations need
	// lexical normalization. Returned CSS remains byte-exact; nonempty content
	// and every other quoted/important declaration still fail the passive parser.
	source = strings.NewReplacer(`content:""`, `content:normal`, `font-size:var(--text-meta)!important`, `font-size:var(--text-meta)`, `font-size:10px!important`, `font-size:10px`, `color:var(--foreground)!important`, `color:var(--foreground)`).Replace(source)
	return validatePassiveStylesheet(source, traceStyleSlots, traceStyleCondition, func(selector, property, value string) bool {
		if passiveStyleOne(value, traceStyleValues[selector+" / "+property]) {
			return true
		}
		switch property {
		case "font-size":
			return passiveStyleDimension(value, 11, 40)
		case "gap", "margin-bottom", "margin-left", "padding-top":
			return passiveStyleDimension(value, 0, 64)
		case "height":
			if selector == ".obs-activity-chart" {
				return passiveStyleDimension(value, 44, 512)
			}
		}
		return false
	})
}
func (v *traceView) StylesheetURL() string {
	if v.WorkbenchManaged {
		return "/api/developer/traces/style?reader_revision=" + url.QueryEscape(v.ReaderRevision)
	}
	return "/trace/legacy-activity-v1.css"
}
func (s *Server) traceStylesheet(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["reader_revision"]) != 1 || !observability.ValidReaderRevision(q.Get("reader_revision")) || q.Get("reader_revision") == "" {
		fail(w, http.StatusBadRequest)
		return
	}
	ref := q.Get("reader_revision")
	reader, ok := s.Traces.(TraceProgramReader)
	if !ok {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	bundle, err := reader.TraceWorkbenchProgram(r.Context(), ref)
	if err != nil || bundle.ReaderRevision != ref || validateTraceStylesheet(bundle.Stylesheet) != nil {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = fmt.Fprint(w, bundle.Stylesheet)
}
func (s *Server) legacyTraceStylesheet(w http.ResponseWriter, r *http.Request) {
	source, err := templates.ReadFile("legacy-ui/trace-v1/trace-activity.css")
	if err != nil {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(source)
}
