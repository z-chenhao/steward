package web

import (
	"github.com/z-chenhao/assistant/internal/memory"
	"html/template"
)

// Request-local materialization of the accepted projection. Missing targets are
// an unavailable managed view, never a native navigation fallback or another RPC.
func memoryPresentationFunctions(v *memoryView) template.FuncMap {
	p := v.Presentation
	return template.FuncMap{
		"memoryURL": func(q memory.InspectionQuery, key, value string) (string, error) {
			if q != v.Query {
				return "", memory.ErrInspectionUnavailable
			}
			for _, link := range p.Links {
				if link.Key == key && link.Value == value {
					return link.URL, nil
				}
			}
			return "", memory.ErrInspectionUnavailable
		},
		"memorySourceLabel": func(kind string) string {
			switch kind {
			case "conversation":
				return p.Source.Conversation
			case "task":
				return p.Source.Task
			case "tool":
				return p.Source.Tool
			case "control":
				return p.Source.Control
			}
			return p.Source.Other
		},
		"memoryOriginLabel": func(kind string) string {
			switch kind {
			case "written":
				return p.Origin.Written
			case "dream":
				return p.Origin.Dream
			case "original":
				return p.Origin.Original
			case "automatic":
				return p.Origin.Automatic
			case "explicit":
				return p.Origin.Explicit
			}
			return p.Origin.Other
		},
		"memoryLayerLabel": func(kind string) string {
			switch kind {
			case memory.Transcripts:
				return p.Layer.Transcripts
			case memory.Representations:
				return p.Layer.Representations
			case memory.Injected:
				return p.Layer.Injected
			}
			return p.Layer.Notes
		},
		"memoryStatus": func(q memory.InspectionQuery) string {
			if q.Status != "" {
				return q.Status
			}
			return p.DefaultStatus
		},
	}
}
