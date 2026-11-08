package ui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// Preview is an ephemeral display, never a delivered artifact or an authority to
// submit an answer. Executable fields and artifact/action references are absent.
type Preview struct {
	Order      string      `json:"-"`
	Title      string      `json:"title"`
	Text       string      `json:"text,omitempty"`
	Components []Component `json:"components"`
}

// StagedPreview carries private recovery metadata, never a model tool call.
type StagedPreview struct {
	Key, Arguments, Order string
}

type previewObserverKey struct{}
type PreviewObserver func(string, string, string)

func WithPreviewObserver(ctx context.Context, observe PreviewObserver) context.Context {
	return context.WithValue(ctx, previewObserverKey{}, observe)
}

// ObservePreview is called outside the staging transaction. The ordinary tool
// completion, reply binding and Delivery remain the only durable authorities.
func ObservePreview(ctx context.Context, key, raw string, order ...string) {
	if observe, ok := ctx.Value(previewObserverKey{}).(PreviewObserver); ok && ctx.Err() == nil {
		position := ""
		if len(order) > 0 {
			position = order[0]
		}
		observe(key, raw, position)
	}
}

// ParsePreview accepts a bounded JSON prefix. Only complete array members are
// shown, except the current components/props object and CSS/HTML string prefix.
// Every native component still passes the owning catalog's validation.
func ParsePreview(raw string) (Preview, bool) {
	var out Preview
	if len(raw) > 128<<10 {
		return out, false
	}
	p := prefixParser{decoder: json.NewDecoder(strings.NewReader(raw)), raw: raw}
	value, complete, ok := p.value("", 0)
	if !ok || complete && !json.Valid([]byte(raw)) {
		return out, false
	}
	root, ok := value.(map[string]any)
	if !ok {
		return out, false
	}
	out.Title, _ = root["title"].(string)
	out.Text, _ = root["text"].(string)
	if utf8.RuneCountInString(out.Text) > 8192 {
		return Preview{}, false
	}
	if utf8.RuneCountInString(out.Title) > 200 {
		return Preview{}, false
	}
	components, _ := root["components"].([]any)
	if len(components) > 16 {
		return Preview{}, false
	}
	for _, value := range components {
		c, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := c["name"].(string)
		props, ok := c["props"].(map[string]any)
		if !ok {
			continue
		}
		if name == "generateSandboxedUi" {
			// Never forward jsFunctions/jsExpressions or inline event handlers to an
			// executable frame. The preview renderer sanitizes HTML in a scriptless frame.
			height, _ := props["initialHeight"].(float64)
			if height < 50 || height > 4000 {
				height = 240
			}
			html, _ := props["html"].(string)
			css, _ := props["css"].(string)
			b, _ := json.Marshal(map[string]any{"initialHeight": height, "html": html, "css": css, "cssComplete": props["css"] != nil})
			out.Components = append(out.Components, Component{Name: name, Props: b})
			continue
		}
		// Empty/default metadata represents fields still arriving, not invented data.
		defaults := map[string]any{}
		switch name {
		case "Table":
			defaults = map[string]any{"title": "", "columns": []any{}, "rows": []any{}, "source": ""}
		case "barChart", "pieChart", "steward.lineChart", "steward.scatterChart":
			defaults = map[string]any{"title": "", "description": ""}
			if _, multi := props["series"]; !multi {
				defaults["data"] = []any{}
			}
		case "plan_visualization":
			defaults = map[string]any{"approach": "", "technology": "", "key_elements": []any{}}
		case "scheduleTime", "steward.form":
			// Complete fields can be previewed, but have no form or message identity.
		case "steward.cards":
			defaults = map[string]any{"items": []any{}}
			if items, ok := props["items"].([]any); ok {
				for _, item := range items {
					if card, ok := item.(map[string]any); ok {
						delete(card, "image_ref")
					}
				}
			}
		default:
			continue // Tool actions wait for delivery.
		}
		for key, value := range defaults {
			if _, exists := props[key]; !exists {
				props[key] = value
			}
		}
		b, err := json.Marshal(props)
		if err != nil {
			continue
		}
		native := Component{Name: name, Props: b}
		if validNative(native) {
			out.Components = append(out.Components, native)
		}
	}
	return out, true
}

// Token decoding preserves JSON escapes, numbers, nesting and complete array
// elements. An unfinished string is decoded separately from its original bytes.
type prefixParser struct {
	decoder *json.Decoder
	raw     string
}

func (p *prefixParser) value(key string, depth int) (any, bool, bool) {
	if depth > 24 {
		return nil, false, false
	}
	offset := p.decoder.InputOffset()
	token, err := p.decoder.Token()
	if err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, false, false
		}
		tail := strings.TrimLeft(p.raw[offset:], " \n\t\r:")
		if strings.HasPrefix(tail, `"`) {
			for cut := 0; cut <= 6 && len(tail)-cut > 0; cut++ {
				var text string
				if json.Unmarshal([]byte(tail[:len(tail)-cut]+`"`), &text) == nil {
					return text, false, true
				}
			}
		}
		return nil, false, true
	}
	switch token {
	case json.Delim('{'):
		out := map[string]any{}
		for p.decoder.More() {
			k, err := p.decoder.Token()
			if err != nil {
				return out, false, true
			}
			name, ok := k.(string)
			if !ok {
				return nil, false, false
			}
			if _, exists := out[name]; exists {
				return nil, false, false
			}
			value, complete, ok := p.value(name, depth+1)
			if !ok {
				return nil, false, false
			}
			if value != nil && (name != "css" || complete) {
				out[name] = value
			}
			if !complete {
				return out, false, true
			}
		}
		end, err := p.decoder.Token()
		return out, err == nil && end == json.Delim('}'), err == nil || err == io.EOF || err == io.ErrUnexpectedEOF
	case json.Delim('['):
		out := []any{}
		for p.decoder.More() {
			value, complete, ok := p.value("", depth+1)
			if !ok {
				return nil, false, false
			}
			if complete || key == "components" && value != nil {
				out = append(out, value)
			}
			if !complete {
				return out, false, true
			}
		}
		end, err := p.decoder.Token()
		return out, err == nil && end == json.Delim(']'), err == nil || err == io.EOF || err == io.ErrUnexpectedEOF
	default:
		if _, number := token.(float64); number && p.decoder.InputOffset() == int64(len(p.raw)) {
			return nil, false, true
		}
		return token, true, true
	}
}
