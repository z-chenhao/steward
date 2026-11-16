package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

// ContextReader reads sensitive frozen input under the existing workspace
// authorization. It has no execution, credential or Artifact-byte access.
type ContextReader interface {
	Rounds(context.Context, observability.RoundQuery) (observability.Rounds, error)
	Context(context.Context, observability.ContextQuery) (observability.FrozenContext, error)
	ContextDiff(context.Context, observability.DiffQuery) (observability.ContextDiff, error)
}

type contextView struct {
	ReaderRevision string
	Query          observability.ContextQuery
	Rounds         observability.Rounds
	Frozen         *observability.FrozenContext
	Diff           *observability.ContextDiff
	RoundAfter     int64
	DiffAfter      int
}

func contextQuery(r *http.Request) (*contextView, error) {
	v := &contextView{Query: observability.ContextQuery{Step: -1, Section: "messages"}, RoundAfter: -1}
	values := r.URL.Query()
	for key, list := range values {
		if len(list) != 1 {
			return nil, observability.ErrInvalid
		}
		switch key {
		case "reader_revision":
			if !observability.ValidReaderRevision(list[0]) {
				return nil, observability.ErrInvalid
			}
			v.ReaderRevision = list[0]
		case "run", "step", "section", "item", "offset", "after", "round_after", "diff_after", "format":
		default:
			return nil, observability.ErrInvalid
		}
	}
	v.Query.RunID = values.Get("run")
	if raw := values.Get("section"); raw != "" {
		v.Query.Section = raw
	}
	if raw := values.Get("format"); raw != "" {
		if raw != "json" {
			return nil, observability.ErrInvalid
		}
		v.Query.JSON = true
	}
	for key, target := range map[string]*int64{"step": &v.Query.Step, "round_after": &v.RoundAfter} {
		if raw := values.Get(key); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				return nil, observability.ErrInvalid
			}
			*target = n
		}
	}
	for key, target := range map[string]*int{"item": &v.Query.Item, "offset": &v.Query.Offset, "after": &v.Query.After, "diff_after": &v.DiffAfter} {
		if raw := values.Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || n > 1<<30 {
				return nil, observability.ErrInvalid
			}
			*target = n
		}
	}
	if (observability.RoundQuery{RunID: v.Query.RunID, After: v.RoundAfter}).Validate() != nil {
		return nil, observability.ErrInvalid
	}
	check := v.Query
	if check.Step == -1 {
		check.Step = 0
	}
	if err := check.Validate(); err != nil {
		return nil, err
	}
	if v.Query.Step == -1 && (v.Query.Item != 0 || v.Query.Offset != 0 || v.Query.After != 0 || v.DiffAfter != 0 || v.Query.JSON || v.Query.Section != "messages") {
		return nil, observability.ErrInvalid
	}
	return v, nil
}

func (s *Server) loadContext(ctx context.Context, v *contextView, api string) error {
	if s.Contexts == nil {
		return errors.New("context inspection unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var err error
	if api == "" || api == "rounds" {
		v.Rounds, err = s.Contexts.Rounds(ctx, observability.RoundQuery{RunID: v.Query.RunID, After: v.RoundAfter})
		if err != nil {
			return err
		}
	}
	if api == "rounds" {
		return nil
	}
	if v.Query.Step < 0 {
		if api != "" {
			return observability.ErrInvalid
		}
		return nil
	}
	if api != "diff" {
		frozen, e := s.Contexts.Context(ctx, v.Query)
		if e != nil {
			return e
		}
		v.Frozen = &frozen
	}
	if api == "diff" || api == "" && (v.Query.Section == "messages" || v.Query.Section == "tools") {
		diff, e := s.Contexts.ContextDiff(ctx, observability.DiffQuery{RunID: v.Query.RunID, Step: v.Query.Step, Section: v.Query.Section, After: v.DiffAfter, ReaderRevision: v.ReaderRevision})
		if e != nil {
			return e
		}
		v.Diff = &diff
		if diff.ReaderRevision != "" {
			v.ReaderRevision = diff.ReaderRevision
		}
	}
	return nil
}

func (s *Server) contextAPI(w http.ResponseWriter, r *http.Request, kind string) {
	v, err := contextQuery(r)
	if err == nil && kind == "rounds" && v.Query.Step >= 0 {
		err = observability.ErrInvalid
	}
	if err == nil && kind == "diff" {
		err = (observability.DiffQuery{RunID: v.Query.RunID, Step: v.Query.Step, Section: v.Query.Section, After: v.DiffAfter, ReaderRevision: v.ReaderRevision}).Validate()
	}
	if err == nil {
		err = s.loadContext(r.Context(), v, kind)
	}
	if err != nil {
		inspectionFailure(w, err)
		return
	}
	switch kind {
	case "rounds":
		jsonResult(w, v.Rounds)
	case "diff":
		jsonResult(w, v.Diff)
	default:
		jsonResult(w, v.Frozen)
	}
}

func contextURL(run string, revision ...string) string {
	link := tracePath(run) + "?tab=context&ctx=1"
	if len(revision) > 0 && revision[0] != "" {
		link += "&reader_revision=" + url.QueryEscape(revision[0])
	}
	return link
}

// Keep section, item and byte pagination separate. Switching a round or section
// clears all dependent offsets, so a stale offset cannot hide a new prompt.
func contextLink(v *contextView, field string, value any) string {
	var n int64
	switch x := value.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case *int:
		n = int64(*x)
	case *int64:
		n = *x
	}
	q := url.Values{"run": {v.Query.RunID}}
	if v.ReaderRevision != "" {
		q.Set("reader_revision", v.ReaderRevision)
	}
	if v.RoundAfter >= 0 {
		q.Set("round_after", strconv.FormatInt(v.RoundAfter, 10))
	}
	if v.Query.Step >= 0 {
		q.Set("step", strconv.FormatInt(v.Query.Step, 10))
		q.Set("section", v.Query.Section)
	}
	q.Set("item", strconv.Itoa(v.Query.Item))
	q.Set("offset", strconv.Itoa(v.Query.Offset))
	q.Set("after", strconv.Itoa(v.Query.After))
	q.Set("diff_after", strconv.Itoa(v.DiffAfter))
	if v.Query.JSON {
		q.Set("format", "json")
	}
	switch field {
	case "step":
		q.Set("step", strconv.FormatInt(n, 10))
		q.Set("section", "messages")
		q.Del("item")
		q.Del("offset")
		q.Del("after")
		q.Del("diff_after")
		q.Del("format")
	case "messages", "tools", "response", "provenance", "recall":
		q.Set("section", field)
		q.Del("item")
		q.Del("offset")
		q.Del("after")
		q.Del("diff_after")
		q.Del("format")
	case "item":
		q.Set("item", strconv.FormatInt(n, 10))
		q.Del("offset")
	case "json":
		q.Set("format", "json")
		q.Del("offset")
	case "text":
		q.Del("format")
		q.Del("offset")
	default:
		q.Set(field, strconv.FormatInt(n, 10))
	}
	q.Del("run")
	q.Set("tab", "context")
	q.Set("ctx", "1")
	return tracePath(v.Query.RunID) + "?" + q.Encode()
}
