package memory

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/recall"
)

// Layers describe representations, independently of the legacy Core/Recall tier.
const (
	Transcripts     = "transcripts"
	Notes           = "notes"
	Representations = "representations"
	Injected        = "injected"
)

func ValidLayer(layer string) bool {
	return layer == Transcripts || DocumentLayer(layer)
}
func DocumentLayer(layer string) bool {
	return layer == Notes || layer == Representations || layer == Injected
}

// Document is a private, storage-independent memory projection. Names are labels,
// never filesystem paths. The Host supplies ownership and original provenance.
type SupportQuote struct {
	Observation int    `json:"observation_index"`
	Quote       string `json:"quote"`
}

type Document struct {
	Evidence  []recall.Evidence `json:"evidence,omitempty"`
	Ref       string            `json:"ref"`
	Layer     string            `json:"layer"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Revision  int64             `json:"revision"`
	Sources   []SourceRef       `json:"sources"`
	Links     []string          `json:"links,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

type DocumentWrite struct {
	Support  []SupportQuote    `json:"support,omitempty"`
	Evidence []recall.Evidence `json:"-"` // Host-validated, never model authority.
	Ref      string            `json:"memory_ref,omitempty"`
	Layer    string            `json:"layer"`
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Revision int64             `json:"revision,omitempty"`
	Links    []string          `json:"links,omitempty"`
	Retire   bool              `json:"retire,omitempty"`
}

func (w DocumentWrite) Valid() bool {
	if len(w.Support) > 16 {
		return false
	}
	for _, q := range w.Support {
		if q.Observation < 0 || len(q.Quote) > 1024 || strings.TrimSpace(q.Quote) == "" || HasSecret(q.Quote) {
			return false
		}
	}
	if !DocumentLayer(w.Layer) || w.Revision < 0 || len(w.Title) > 256 || len(w.Content) > 48<<10 || !utf8.ValidString(w.Title+w.Content) || HasSecret(w.Title+"\n"+w.Content) || len(w.Links) > 32 {
		return false
	}
	if w.Ref == "" && (w.Revision != 0 || w.Retire) || w.Ref != "" && (!ValidInspectionRef(w.Ref) || w.Revision < 1) {
		return false
	}
	if !w.Retire && (strings.TrimSpace(w.Title) == "" || strings.TrimSpace(w.Content) == "") {
		return false
	}
	for _, link := range w.Links {
		if !ValidInspectionRef(link) {
			return false
		}
	}
	return true
}

// Documents is consumed by the runtime; SQLite is one adapter. No path, SQL,
// model-selected identity or local file access crosses this seam.
type Documents interface {
	ListDocumentPage(context.Context, recall.Scope, int64, string, int, string) ([]Document, string, error)
	ListDocuments(context.Context, recall.Scope, int64, string, int) ([]Document, error)
	ReadDocument(context.Context, recall.Scope, int64, string) (Document, error)
	WriteDocument(context.Context, recall.Scope, int64, string, string, time.Time, []SourceRef, DocumentWrite) (Document, error)
}

// NavigationReader returns only authorized labels and bounded search terms. It
// must not fetch document bodies merely to choose which references to expose.
type NavigationReader interface {
	ReadNavigation(context.Context, recall.Scope, int64, []string) ([]NavigationEntry, error)
}

type NavigationEntry struct {
	Ref, Title string
	Terms      []string
}

func InjectedText(text string) string {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 200 {
		text = strings.Join(lines[:200], "")
	}
	if len(text) > 25_000 {
		text = text[:25_000]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}
