// Package ui owns versioned, controlled Delivery projections. Models only emit
// semantic Conversation content; this package never interprets executable markup.
package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"html/template"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/llm"
)

const Version = 1

type Field struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Value string `json:"value"`
}
type Component struct {
	ActionView     *contract.ActionView  `json:"-"`
	ActionManaged  bool                  `json:"action_view,omitempty"`
	MeetingView    *contract.MeetingView `json:"-"`
	MeetingManaged bool                  `json:"meeting_view,omitempty"`
	// Authorized server-side display only; never part of frozen identity or wire.
	FormView    *FormView       `json:"-"`
	Title       string          `json:"title,omitempty"`
	WidgetRef   string          `json:"widget_ref,omitempty"`
	ViewRef     string          `json:"view_ref,omitempty"`
	Type        string          `json:"type,omitempty"`
	Name        string          `json:"name,omitempty"`
	Props       json.RawMessage `json:"props,omitempty"`
	Response    string          `json:"response,omitempty"`
	Text        string          `json:"text,omitempty"`
	ArtifactRef string          `json:"artifact_ref,omitempty"`
	Columns     []string        `json:"columns,omitempty"`
	Rows        [][]string      `json:"rows,omitempty"`
	Status      string          `json:"status,omitempty"`
	ApprovalID  string          `json:"approval_id,omitempty"`
	Hash        string          `json:"frozen_arguments_hash,omitempty"`
	Fields      []Field         `json:"fields,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	FormID      string          `json:"form_id,omitempty"`
	Submitted   bool            `json:"submitted,omitempty"`
	Disabled    bool            `json:"disabled,omitempty"`
}
type Spec struct {
	ActionViews   map[string]contract.ActionView  `json:"action_views,omitempty"`
	MeetingViews  map[string]contract.MeetingView `json:"meeting_views,omitempty"`
	SchemaVersion int                             `json:"schema_version"`
	Fallback      string                          `json:"fallback"`
	Components    []Component                     `json:"components"`
}

func Hash(v any) string {
	raw, _ := llm.CanonicalJSON(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func Compose(e delivery.Envelope) Spec {
	s := Spec{SchemaVersion: Version, Components: []Component{}}
	forms := 0
	for _, p := range e.Message.Content {
		if p.Type == "text" {
			parts, fallback := composeText(e.Message, p.Text, len(s.Components))
			count := 0
			for _, c := range parts {
				if c.Type == "form" {
					count++
				}
			}
			if forms+count > 4 || len(s.Components)+len(parts) > 128 {
				parts = []Component{{Type: "markdown", Text: fallback}}
			} else {
				forms += count
			}
			s.Components = append(s.Components, parts...)
			s.Fallback += fallback + "\n"
		} else if p.ArtifactRef != "" {
			s.Components = append(s.Components, Component{Type: "artifact", Text: p.Type, ArtifactRef: p.ArtifactRef})
			s.Fallback += p.Type + ": " + p.ArtifactRef + "\n"
		}
	}
	s.Fallback = strings.TrimSpace(s.Fallback)
	return s
}

// ValidComponent checks the controlled semantic component grammar.
func ValidComponent(c Component) bool {
	if len(c.Text) > 128<<10 {
		return false
	}
	switch c.Type {
	case "openintelligentui":
		return validNative(c)
	case "widget":
		return strings.HasPrefix(c.WidgetRef, "ui:") && len(c.WidgetRef) < 256 && !strings.ContainsAny(c.WidgetRef, "/\\ \r\n") && c.ApprovalID == ""
	case "markdown", "progress":
		return c.ArtifactRef == "" && c.ApprovalID == ""
	case "artifact":
		return strings.HasPrefix(c.ArtifactRef, "artifact:") && !strings.ContainsAny(c.ArtifactRef, "/\\ \r\n\x00")
	case "table":
		if len(c.Columns) == 0 || len(c.Columns) > 32 || len(c.Rows) > 256 {
			return false
		}
		for _, r := range c.Rows {
			if len(r) != len(c.Columns) {
				return false
			}
		}
		for _, column := range c.Columns {
			if len(column) > 8192 {
				return false
			}
		}
		for _, row := range c.Rows {
			for _, cell := range row {
				if len(cell) > 8192 {
					return false
				}
			}
		}
		return true
	case "confirmation":
		return c.ApprovalID != "" && len(c.Hash) == 64
	case "form":
		return validFields(c.Fields) && (c.FormID == "" || c.MessageID != "" && len(c.FormID) == 64)
	}
	return false
}

var component = template.Must(template.New("component").Parse(`{{if eq .Type "widget"}}<section class="intelligent-widget"><h3>{{.Title}}</h3><p class="semantic">{{.Text}}</p></section>{{else if eq .Type "markdown"}}<div class="semantic">{{.Text}}</div>{{else if eq .Type "artifact"}}<a class="artifact" href="/artifact?ref={{.ArtifactRef}}">↓ {{.Text}} attachment</a>{{else if eq .Type "progress"}}<p class="progress">{{.Status}} · {{.Text}}</p>{{else if eq .Type "table"}}<table><thead><tr>{{range .Columns}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table>{{else if eq .Type "confirmation"}}<section class="approval"><p class="semantic">{{.Text}}</p><form action="/approval" method="post"><input type="hidden" name="approval_id" value="{{.ApprovalID}}"><input type="hidden" name="hash" value="{{.Hash}}"><button name="decision" value="approved">Approve</button><button class="secondary" name="decision" value="denied">Decline</button></form></section>{{else if eq .Type "form"}}<dl>{{range .Fields}}<dt>{{.Label}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}`))

// HTML's only trusted source is this fixed escaped template. A component failure
// degrades locally. Unknown versions and malformed projections retain semantics.
func HTML(raw json.RawMessage, fallback string) template.HTML {
	return HTMLWithFormDisplays(raw, fallback, nil)
}

// HTMLWithFormDisplays accepts only Host-authorized display data from the
// private Web projection envelope; it never changes the serialized component.
func HTMLWithFormDisplays(raw json.RawMessage, fallback string, displays map[string]FormView) template.HTML {
	var s Spec
	if json.Unmarshal(raw, &s) != nil || s.SchemaVersion != Version || len(s.Components) > 128 {
		return template.HTML(template.HTMLEscapeString(fallback))
	}
	var out bytes.Buffer
	for _, c := range s.Components {
		if c.MeetingManaged {
			if view, ok := s.MeetingViews[c.FormID]; ok {
				c.MeetingView = &view
			}
		}
		if c.Name == "steward.form" && c.ViewRef != "" && c.FormID != "" {
			if view, ok := displays[c.FormID]; ok {
				c.FormView = &view
			}
		}
		if !ValidComponent(c) {
			out.WriteString(`<p class="semantic">` + template.HTMLEscapeString(fallback) + `</p>`)
			continue
		}
		if c.Type == "openintelligentui" {
			out.WriteString(nativeHTML(c))
			continue
		}
		var b bytes.Buffer
		renderer := component
		if c.Type == "form" && c.FormID != "" {
			renderer = formComponent
		}
		if renderer.Execute(&b, c) != nil {
			out.WriteString(template.HTMLEscapeString(fallback))
		} else {
			out.Write(b.Bytes())
		}
	}
	if len(s.Components) == 0 {
		out.WriteString(template.HTMLEscapeString(fallback))
	}
	return template.HTML(out.String())
}

// Card maps only supported safe components to Feishu's controlled card schema.
// Unsupported or malformed components retain their semantic fallback.
func Card(s Spec) map[string]any {
	text := s.Fallback
	if s.SchemaVersion == Version {
		parts := []string{}
		for _, c := range s.Components {
			if !ValidComponent(c) {
				parts = append(parts, s.Fallback)
				continue
			}
			switch c.Type {
			case "markdown", "progress", "openintelligentui":
				parts = append(parts, c.Text)
			case "table":
				parts = append(parts, strings.Join(c.Columns, " | "))
				for _, r := range c.Rows {
					parts = append(parts, strings.Join(r, " | "))
				}
			case "confirmation":
				parts = append(parts, c.Text+"\nApproval: "+c.ApprovalID+"\nHash: "+c.Hash+"\nReply /approve ID HASH or /deny ID HASH")
			case "form":
				if c.Text != "" {
					parts = append(parts, c.Text)
				}
				for _, f := range c.Fields {
					parts = append(parts, f.Label+": "+f.Value)
				}
			}
		}
		if len(parts) > 0 {
			text = strings.Join(parts, "\n")
		}
	}
	// plain_text prevents provider markdown mentions/URLs from acquiring actions.
	return map[string]any{"config": map[string]any{"wide_screen_mode": true}, "elements": []any{map[string]any{"tag": "div", "text": map[string]string{"tag": "plain_text", "content": text}}}}
}
