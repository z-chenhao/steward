package web

import (
	"context"
	"net/http"
	"net/url"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type formViewReader interface {
	WebFormView(context.Context, string) (ui.FormView, error)
}

// Pure display data for the existing native controls. No candidate-selected
// ingress, message/form identities, code or resource URLs enter this response.
func (s *Server) formView(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["ref"]) != 1 || query.Get("ref") == "" {
		fail(w, 400)
		return
	}
	reader, ok := s.Core.(formViewReader)
	if !ok {
		fail(w, 503)
		return
	}
	view, err := reader.WebFormView(r.Context(), query.Get("ref"))
	if err != nil {
		fail(w, 404)
		return
	}
	jsonResult(w, view)
}
