package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

// This endpoint projects local display snapshots only. It cannot read credentials,
// alter a selection, or promote client state into authoritative configuration.
func (s *Server) composerModelProjection(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var facts contract.BrowserFacts
	if decoder.Decode(&facts) != nil || decoder.Decode(new(any)) != io.EOF || !validBrowserModelFacts(facts) {
		fail(w, 400)
		return
	}
	reference := r.Header.Get("X-Steward-Model-Reader")
	if !validModelReader(reference) {
		fail(w, 400)
		return
	}
	input := contract.Facts{ReaderRevision: reference, Candidates: []contract.Candidate{}, Catalogues: []contract.Catalogue{}, Browser: &facts}
	var out contract.Projection
	managed := false
	var err error
	if s.ModelMenu != nil {
		out, managed, err = s.ModelMenu(r.Context(), input)
	}
	if err != nil {
		fail(w, 503)
		return
	}
	if !managed {
		out = legacyModelMenuV1(input)
	}
	choices, err := bindBrowserModelOrder(facts, out)
	if err != nil {
		fail(w, 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResult(w, choices)
}
func validBrowserModelFacts(f contract.BrowserFacts) bool {
	if f.Reason != "refresh" && f.Reason != "ack" || len(f.Previous)+len(f.Received) > 4096 {
		return false
	}
	for _, list := range [][]contract.BrowserChoice{f.Previous, f.Received} {
		for _, c := range list {
			if c.Key == "" || !utf8.ValidString(c.Key) || !utf8.ValidString(c.Label) || !utf8.ValidString(c.SortKey) || strings.ContainsRune(c.Key, 0) || strings.ContainsRune(c.Label, 0) || strings.ContainsRune(c.SortKey, 0) {
				return false
			}
		}
	}
	return true
}
func bindBrowserModelOrder(f contract.BrowserFacts, p contract.Projection) ([]contract.Choice, error) {
	if p.Order == nil || len(*p.Order) > len(f.Previous)+len(f.Received) || p.Current != "" || len(p.Choices) != 0 || p.Unavailable {
		return nil, llm.ErrConflict
	}
	choices := []contract.Choice{}
	seen := map[string]bool{}
	for _, ref := range *p.Order {
		var source []contract.BrowserChoice
		switch ref.Source {
		case "previous":
			source = f.Previous
		case "received":
			source = f.Received
		default:
			return nil, llm.ErrConflict
		}
		if ref.Index < 0 || ref.Index >= len(source) {
			return nil, llm.ErrConflict
		}
		c := source[ref.Index].Choice
		if seen[c.Key] {
			return nil, llm.ErrConflict
		}
		seen[c.Key] = true
		choices = append(choices, c)
	}
	return choices, nil
}
