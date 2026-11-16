package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type cardsLinkReader interface {
	WebCardsLink(context.Context, string, int) (string, error)
}

func (s *Server) cardsLink(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.Core.(cardsLinkReader)
	if !ok {
		fail(w, 503)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["ref"]) != 1 || len(query["item"]) != 1 {
		fail(w, 400)
		return
	}
	item, err := strconv.Atoi(query.Get("item"))
	if err != nil || item < 0 || item > 15 || strconv.Itoa(item) != query.Get("item") {
		fail(w, 400)
		return
	}
	destination, err := reader.WebCardsLink(r.Context(), query.Get("ref"), item)
	if err != nil {
		fail(w, 404)
		return
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		fail(w, 404)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, destination, http.StatusFound)
}
