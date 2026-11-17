package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

type MemoryReader interface {
	InspectMemory(context.Context, memory.InspectionQuery) (memory.InspectionPage, error)
}
type MemoryPresentedReader interface {
	InspectMemoryPresented(context.Context, memory.InspectionQuery, string) (memory.InspectionPage, *memory.InspectionPresentation, error)
}

type memoryView struct {
	Presentation        *memory.InspectionPresentation
	Query               memory.InspectionQuery
	Page                memory.InspectionPage
	Selected            *memory.InspectionRecord
	MaintenanceSelected *memory.MaintenanceRecord
}

func memoryQuery(r *http.Request) (memory.InspectionQuery, error) {
	q := memory.InspectionQuery{}
	values := r.URL.Query()
	for key, items := range values {
		if len(items) != 1 {
			return q, memory.ErrInspectionInvalid
		}
		switch key {
		case "layer", "ref", "search", "tier", "origin", "status", "view", "cursor", "limit", "source", "offset", "since", "until", "sources_after":
		default:
			return q, memory.ErrInspectionInvalid
		}
	}
	q.Layer = values.Get("layer")
	q.Ref = values.Get("ref")
	q.Search = values.Get("search")
	q.Tier = values.Get("tier")
	q.Origin = values.Get("origin")
	q.Status = values.Get("status")
	q.View = values.Get("view")
	q.Cursor = values.Get("cursor")
	q.Source = values.Get("source")
	if v, ok := values["limit"]; ok {
		n, e := strconv.Atoi(v[0])
		if e != nil || n < 1 {
			return q, memory.ErrInspectionInvalid
		}
		q.Limit = n
	}
	if v, ok := values["offset"]; ok {
		n, e := strconv.ParseInt(v[0], 10, 64)
		if e != nil {
			return q, memory.ErrInspectionInvalid
		}
		q.Offset = n
	}
	if v, ok := values["sources_after"]; ok {
		n, e := strconv.Atoi(v[0])
		if e != nil {
			return q, memory.ErrInspectionInvalid
		}
		q.SourcesOffset = n
	}
	for key, target := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if value := values.Get(key); value != "" {
			var err error
			*target, err = inspectionTimeIn(value, filterLocation(r))
			if err != nil {
				return q, memory.ErrInspectionInvalid
			}
		}
	}
	return q, q.Validate()
}
func (s *Server) loadMemory(ctx context.Context, r *http.Request) (*memoryView, error) {
	queryRequest := r
	reference := ""
	usePresentation := !strings.HasPrefix(r.URL.Path, "/api/")
	if usePresentation {
		if values, ok := r.URL.Query()["inspector_revision"]; ok {
			if len(values) != 1 || values[0] == "" || len(values[0]) > 256 {
				return nil, memory.ErrInspectionInvalid
			}
			reference = values[0]
			clone := r.Clone(ctx)
			copied := *r.URL
			clone.URL = &copied
			query := copied.Query()
			query.Del("inspector_revision")
			clone.URL.RawQuery = query.Encode()
			queryRequest = clone
		}
	}
	q, err := memoryQuery(queryRequest)
	if err != nil {
		return nil, err
	}
	if s.Memory == nil {
		return nil, memory.ErrInspectionUnavailable
	}
	var p memory.InspectionPage
	var presentation *memory.InspectionPresentation
	if reader, ok := s.Memory.(MemoryPresentedReader); usePresentation && ok {
		p, presentation, err = reader.InspectMemoryPresented(ctx, q, reference)
	} else {
		if reference != "" {
			return nil, memory.ErrInspectionUnavailable
		}
		p, err = s.Memory.InspectMemory(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	v := &memoryView{Query: q, Page: p, Presentation: presentation}
	if q.Ref != "" && q.View == "maintenance" {
		if len(p.Maintenance) != 1 {
			return nil, memory.ErrInspectionNotFound
		}
		v.MaintenanceSelected = &v.Page.Maintenance[0]
		return v, nil
	}
	if q.Ref != "" {
		if len(p.Items) != 1 {
			return nil, memory.ErrInspectionNotFound
		}
		v.Selected = &v.Page.Items[0]
	}
	return v, nil
}
func (s *Server) memoryAPI(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadMemory(r.Context(), r)
	if err != nil {
		code := 503
		if errors.Is(err, memory.ErrInspectionInvalid) {
			code = 400
		}
		if errors.Is(err, memory.ErrInspectionNotFound) {
			code = 404
		}
		fail(w, code)
		return
	}
	jsonResult(w, v.Page)
}
func memoryOffset(n *int64) string {
	if n == nil {
		return "0"
	}
	return strconv.FormatInt(*n, 10)
}

func memorySourcesOffset(n *int) string {
	if n == nil {
		return "0"
	}
	return strconv.Itoa(*n)
}
