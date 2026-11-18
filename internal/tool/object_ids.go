package tool

import (
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/jsonwire"
)

// ObjectID is the model-facing spelling of a Host-owned object identity. The
// qualified storage key stays private; this spelling never grants access.
func ObjectID(ref string) string {
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || id == "" || strings.ContainsAny(id, ":/\\ \t\r\n\x00") {
		return ref
	}
	switch kind {
	case "conversation", "artifact", "task", "memory", "feedback", "run", "trace", "recall", "ui", "memory-event", "automation", "effect", "generation":
		return id
	}
	return ref
}

// ObjectRef resolves an ID only in the owning operation's known domain. It
// accepts historical qualified keys for replay, never guesses an object kind.
func ObjectRef(kind, id string) string {
	if id == "" || strings.Contains(id, ":") {
		return id
	}
	return kind + ":" + id
}

func ObjectIDs(refs []string) []string {
	if refs == nil {
		return nil
	}
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ObjectID(ref)
	}
	return out
}

// ModelObjectIDs projects identity fields in Host business envelopes. Evidence
// bodies, user text, file paths and external URLs are never rewritten.
func ModelObjectIDs(raw string) string {
	var value any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	// Preserve ambiguous JSON verbatim instead of collapsing duplicate evidence
	// keys or recursing through an unbounded payload during presentation.
	if jsonwire.Check(d) != nil {
		return raw
	}
	d = json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return raw
	}
	changed := false
	var visit func(any, bool) any
	visit = func(v any, identity bool) any {
		switch x := v.(type) {
		case string:
			if identity {
				id := ObjectID(x)
				changed = changed || id != x
				return id
			}
		case []any:
			for i := range x {
				x[i] = visit(x[i], identity)
			}
		case map[string]any:
			for key, item := range x {
				switch key {
				case "ref", "source_ref", "source_refs", "taskRef", "task_ref", "artifactRef", "artifact_ref", "artifactRefs", "artifact_refs", "imageRef", "image_ref", "image_refs", "memory_ref", "supersedes", "feedbackRef", "feedback_ref", "history_artifact", "archive_ref", "document_ref", "ui_ref", "widget_ref", "component_ref", "receipt", "automationRef":
					x[key] = visit(item, true)
				case "text", "quote", "task", "instruction", "prompt", "summary", "snippet", "context", "model_context", "modelContext", "state", "arguments", "input_schema", "schema":
					// Opaque evidence and exact action/schema payloads are not Host
					// object envelopes, even if their fields happen to be named ref.
				case "content":
					// Original evidence is not a transport identity field.
					switch item.(type) {
					case map[string]any, []any:
						x[key] = visit(item, false)
					}
				default:
					x[key] = visit(item, false)
				}
			}
		}
		return v
	}
	visit(value, false)
	if !changed {
		return raw
	}
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(out)
}
