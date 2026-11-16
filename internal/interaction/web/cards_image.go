package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type cardsImageReader interface {
	OpenWebCardsImage(context.Context, string, int) (io.ReadCloser, string, error)
}

func (s *Server) cardsImage(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.Core.(cardsImageReader)
	if !ok {
		fail(w, 503)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400)
		return
	}
	if len(query) != 2 || len(query["ref"]) != 1 || len(query["item"]) != 1 {
		fail(w, 400)
		return
	}
	item, err := strconv.Atoi(query.Get("item"))
	if err != nil || item < 0 || item > 15 || strconv.Itoa(item) != query.Get("item") {
		fail(w, 400)
		return
	}
	source, mediaType, err := reader.OpenWebCardsImage(r.Context(), query.Get("ref"), item)
	if err != nil {
		fail(w, 404)
		return
	}
	defer source.Close()
	switch mediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		fail(w, 415)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	_, _ = io.Copy(w, source)
}
