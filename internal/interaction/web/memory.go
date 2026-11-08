package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

type MemoryReader interface {
	InspectMemory(context.Context, memory.InspectionQuery) (memory.InspectionPage, error)
}
type memoryView struct {
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
	q, err := memoryQuery(r)
	if err != nil {
		return nil, err
	}
	if s.Memory == nil {
		return nil, memory.ErrInspectionUnavailable
	}
	p, err := s.Memory.InspectMemory(ctx, q)
	if err != nil {
		return nil, err
	}
	v := &memoryView{Query: q, Page: p}
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
func memoryURL(q memory.InspectionQuery, key, value string) string {
	switch key {
	case "layer":
		q = memory.InspectionQuery{Layer: value}
	case "ref":
		if q.View == "maintenance" {
			value = strings.Replace(value, "memory-event:", "memory:maintenance-", 1)
		}
		q.Ref = value
		q.Cursor = ""
		q.Source = ""
		q.Offset = 0
		q.SourcesOffset = 0
		if q.View != "maintenance" {
			q.View = ""
		}
	case "source":
		q.Source = value
		q.Offset = 0
	case "sources_after":
		q.SourcesOffset, _ = strconv.Atoi(value)
		q.Source = ""
		q.Offset = 0
	case "offset":
		q.Offset, _ = strconv.ParseInt(value, 10, 64)
	case "cursor":
		q.Cursor = value
	case "view":
		q = memory.InspectionQuery{View: value}
	}
	values := url.Values{}
	for key, value := range map[string]string{"layer": q.Layer, "ref": q.Ref, "search": q.Search, "tier": q.Tier, "origin": q.Origin, "status": q.Status, "view": q.View, "cursor": q.Cursor, "source": q.Source} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if !q.Since.IsZero() {
		values.Set("since", q.Since.Format(time.RFC3339))
	}
	if !q.Until.IsZero() {
		values.Set("until", q.Until.Format(time.RFC3339))
	}
	if q.Offset != 0 {
		values.Set("offset", strconv.FormatInt(q.Offset, 10))
	}
	if q.SourcesOffset != 0 {
		values.Set("sources_after", strconv.Itoa(q.SourcesOffset))
	}
	if q.Limit != 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(values) == 0 {
		return "/developer/memory"
	}
	return "/developer/memory?" + values.Encode()
}
func memorySourceLabel(kind string) string {
	switch kind {
	case "conversation":
		return "Source message"
	case "task":
		return "Source task"
	case "tool":
		return "memory_manage"
	case "control":
		return "Local control"
	}
	return "Source"
}
func memoryOriginLabel(origin string) string {
	switch origin {
	case "written":
		return "Written note"
	case "dream":
		return "Dreaming"
	case "original":
		return "Original record"
	case "automatic":
		return "Automatically extracted"
	case "explicit":
		return "Explicitly remembered"
	}
	return "Unknown origin"
}
func memoryStatus(q memory.InspectionQuery) string {
	if q.Status == "" {
		return "active"
	}
	return q.Status
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

func memoryLayerLabel(layer string) string {
	switch layer {
	case memory.Transcripts:
		return "Transcripts"
	case memory.Representations:
		return "Memory representations"
	case memory.Injected:
		return "Injected context"
	default:
		return "Memory notes"
	}
}
