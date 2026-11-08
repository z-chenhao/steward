package memory

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInspectionInvalid = errors.New("invalid memory inspection")
var ErrInspectionNotFound = errors.New("memory inspection not found")
var ErrInspectionUnavailable = errors.New("memory inspection unavailable")

// Inspection is a private owner-facing read projection, independent of recall
// rankings and Tool results. It never grants access by accepting a scope.
type InspectionQuery struct {
	Layer         string    `json:"layer,omitempty"`
	SourcesOffset int       `json:"sources_after,omitempty"`
	Since         time.Time `json:"since,omitempty"`
	Until         time.Time `json:"until,omitempty"`
	Ref           string    `json:"ref,omitempty"`
	Search        string    `json:"search,omitempty"`
	Tier          string    `json:"tier,omitempty"`
	Origin        string    `json:"origin,omitempty"`
	Status        string    `json:"status,omitempty"`
	View          string    `json:"view,omitempty"`
	Cursor        string    `json:"cursor,omitempty"`
	Limit         int       `json:"limit,omitempty"`
	Source        string    `json:"source,omitempty"`
	Offset        int64     `json:"offset,omitempty"`
}

func (q InspectionQuery) Validate() error {
	if q.Layer == Transcripts && (q.Tier != "" || q.Origin != "" || q.Status != "" || q.View != "") || q.Layer != "" && q.View != "" {
		return ErrInspectionInvalid
	}
	if q.Layer != "" && !ValidLayer(q.Layer) {
		return ErrInspectionInvalid
	}
	if q.SourcesOffset < 0 || q.SourcesOffset > 1000000 || q.Ref == "" && q.SourcesOffset != 0 {
		return ErrInspectionInvalid
	}
	if !q.Since.IsZero() && !q.Until.IsZero() && !q.Since.Before(q.Until) {
		return ErrInspectionInvalid
	}
	if !utf8.ValidString(q.Search) || len(q.Search) > 200 || strings.ContainsAny(q.Search, "\x00\r\n") || len(q.Cursor) > 1024 || q.Limit < 0 || q.Limit > 50 || q.Offset < 0 || q.Offset > 1<<30 {
		return ErrInspectionInvalid
	}
	if q.Tier != "" && q.Tier != "core" && q.Tier != "recall" || q.Origin != "" && q.Origin != "automatic" && q.Origin != "explicit" && q.Origin != "unknown" && q.Origin != "written" && q.Origin != "dream" || q.Status != "" && q.Status != "active" && q.Status != "superseded" && q.Status != "retired" && q.Status != "all" || q.View != "" && q.View != "maintenance" {
		return ErrInspectionInvalid
	}
	if q.Ref != "" && (!ValidInspectionRef(q.Ref) || q.Cursor != "" || q.View != "" && !(q.View == "maintenance" && strings.HasPrefix(q.Ref, "memory:maintenance-"))) || q.Source != "" && (q.Ref == "" || len(q.Source) > 256 || strings.ContainsAny(q.Source, " /\\\r\n\x00")) || q.Source == "" && q.Offset != 0 {
		return ErrInspectionInvalid
	}
	if q.View == "maintenance" && q.Ref != "" && !strings.HasPrefix(q.Ref, "memory:maintenance-") {
		return ErrInspectionInvalid
	}
	if q.View == "maintenance" && (q.Search != "" || q.Tier != "" || q.Origin != "" || q.Status != "" || q.Source != "" || !q.Since.IsZero() || !q.Until.IsZero()) {
		return ErrInspectionInvalid
	}
	return nil
}
func ValidInspectionRef(ref string) bool {
	id, ok := strings.CutPrefix(ref, "memory:")
	if !ok || id == "" || len(ref) > 256 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == ':') {
			return false
		}
	}
	return true
}
func (q InspectionQuery) PageSize() int {
	if q.Limit == 0 {
		return 20
	}
	return q.Limit
}
func (q InspectionQuery) CursorBinding(owner string, floor int64) string {
	q.Cursor = ""
	q.Limit = 0
	raw, _ := json.Marshal(struct {
		Owner string
		Floor int64
		Query InspectionQuery
	}{owner, floor, q})
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

type InspectionPosition struct {
	Binding string `json:"b"`
	At      int64  `json:"a"`
	Ref     string `json:"r"`
}

func (q InspectionQuery) Position(owner string, floor int64) (InspectionPosition, error) {
	var p InspectionPosition
	if q.Cursor == "" {
		return p, nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
	if e != nil || json.Unmarshal(raw, &p) != nil || p.Binding != q.CursorBinding(owner, floor) || p.At < 0 || !ValidInspectionRef(p.Ref) {
		return p, ErrInspectionInvalid
	}
	return p, nil
}
func (q InspectionQuery) Next(owner string, floor, at int64, ref string) string {
	raw, _ := json.Marshal(InspectionPosition{q.CursorBinding(owner, floor), at, ref})
	return base64.RawURLEncoding.EncodeToString(raw)
}

type InspectionSource struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	RunID      string `json:"run_id,omitempty"`
	Text       string `json:"text,omitempty"`
	NextOffset *int64 `json:"next_offset,omitempty"`
}
type InspectionRecord struct {
	Related           []InspectionRelated `json:"related,omitempty"`
	Layer             string              `json:"layer"`
	Title             string              `json:"title,omitempty"`
	Revision          int64               `json:"revision,omitempty"`
	Links             []string            `json:"links,omitempty"`
	SourceCount       int                 `json:"source_count"`
	NextSourcesOffset *int                `json:"next_sources_offset,omitempty"`
	Ref               string              `json:"ref"`
	Content           string              `json:"content,omitempty"`
	ContentState      string              `json:"content_state"`
	Tier              string              `json:"tier"`
	Status            string              `json:"status"`
	Origin            string              `json:"origin"`
	CreatedAt         time.Time           `json:"created_at"`
	Sources           []InspectionSource  `json:"sources"`
	Supersedes        string              `json:"supersedes,omitempty"`
	ReplacedBy        string              `json:"replaced_by,omitempty"`
}

type InspectionRelated struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}
type MaintenanceRecord struct {
	Metrics   *MaintenanceMetrics `json:"metrics,omitempty"`
	Ref       string              `json:"ref"`
	Operation string              `json:"operation"`
	Status    string              `json:"status"`
	CreatedAt time.Time           `json:"created_at"`
	Attempts  int                 `json:"attempts"`
	Error     string              `json:"error,omitempty"`
	RunID     string              `json:"run_id,omitempty"`
}
type InspectionPage struct {
	Items       []InspectionRecord  `json:"items"`
	Maintenance []MaintenanceRecord `json:"maintenance,omitempty"`
	Total       int                 `json:"total"`
	Next        string              `json:"next,omitempty"`
}
