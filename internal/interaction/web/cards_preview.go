package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Reads only the current private preview. Candidate execution happens earlier
// under the Run claim; an HTTP request cannot re-create an ended preview.
func (s *Server) cardsPreview(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.URL.Query().Get("component"))
	requestedRevision, revisionErr := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revisionErr != nil || index < 0 || strconv.Itoa(index) != r.URL.Query().Get("component") {
		fail(w, 404)
		return
	}
	p := &s.uiPreviews
	p.mu.Lock()
	revision, runID := p.frame.Revision, p.runID
	var view ui.CardsView
	found := false
	if p.frame.State == "draft" && p.frame.Key == r.URL.Query().Get("key") && revision == requestedRevision {
		for _, item := range p.frame.Items {
			if item.Key == r.URL.Query().Get("item") && item.CardsManaged {
				view, found = item.CardsViews[index]
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
	current := p.frame.State == "draft" && p.frame.Key == r.URL.Query().Get("key") && p.frame.Revision == revision && p.runID == runID
	p.mu.Unlock()
	if !current {
		fail(w, 404)
		return
	}
	renderReadonlyView(w, r, ui.PlanView{HTML: view.HTML, CSS: view.CSS, Styles: styles, CompactMaxWidth: view.CompactMaxWidth}, true, "Cards preview", cardsMarkup(view.HTML))
}
