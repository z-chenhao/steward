package web

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"io"
	"net/http"
)

type demonstrationReviewer interface {
	ReviewDemonstration(context.Context, contract.DemoRequest) (contract.DemoResult, error)
}

func (s *Server) reviewDemonstration(w http.ResponseWriter, r *http.Request) {
	reviewer, ok := s.Core.(demonstrationReviewer)
	if !ok {
		fail(w, 503)
		return
	}
	var q contract.DemoRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || d.Decode(&struct{}{}) != io.EOF {
		fail(w, 400)
		return
	}
	out, err := reviewer.ReviewDemonstration(r.Context(), q)
	if err != nil {
		fail(w, 503)
		return
	}
	jsonResult(w, out)
}
