package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Reads only the current private preview. Candidate execution happens earlier
// under the Run claim; an HTTP request cannot re-create an ended preview.
func (s *Server) tablePreview(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400)
		return
	}
	for _, key := range []string{"key", "item", "component", "revision"} {
		if len(query[key]) != 1 {
			fail(w, 400)
			return
		}
	}
	for key, values := range query {
		if len(values) != 1 || key != "key" && key != "item" && key != "component" && key != "revision" && key != "theme" {
			fail(w, 400)
			return
		}
	}
	index, err := strconv.Atoi(query.Get("component"))
	requestedRevision, revisionErr := strconv.ParseUint(query.Get("revision"), 10, 64)
	if err != nil || revisionErr != nil || index < 0 || index > 15 || strconv.FormatUint(requestedRevision, 10) != query.Get("revision") || strconv.Itoa(index) != query.Get("component") {
		fail(w, 404)
		return
	}
	p := &s.uiPreviews
	p.mu.Lock()
	revision, runID := p.frame.Revision, p.runID
	var view ui.TableView
	found := false
	if p.frame.State == "draft" && p.frame.Key == query.Get("key") && revision == requestedRevision {
		for _, item := range p.frame.Items {
			if item.Key == query.Get("item") && item.TableManaged {
				view, found = item.TableViews[index]
				break
			}
		}
	}
	p.mu.Unlock()
	if !found {
		fail(w, 404)
		return
	}
	reader, ok := s.Core.(interface {
		UIPreviewStyles(context.Context, string) ([]byte, bool, error)
	})
	if !ok {
		fail(w, 503)
		return
	}
	styles, managed, err := reader.UIPreviewStyles(r.Context(), runID)
	if err != nil || !managed || len(styles) == 0 {
		fail(w, 503)
		return
	}
	p.mu.Lock()
	current := p.frame.State == "draft" && p.frame.Key == query.Get("key") && p.frame.Revision == revision && p.runID == runID
	p.mu.Unlock()
	if !current {
		fail(w, 404)
		return
	}
	view.Styles = styles
	renderTableFrame(w, r, view, true)
}
