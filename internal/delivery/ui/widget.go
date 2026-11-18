package ui

import (
	"encoding/json"
	"fmt"

	"github.com/z-chenhao/assistant/internal/delivery/uidocv1"
	"github.com/z-chenhao/assistant/internal/tool"
)

const ShowName = "builtin__intelligent_ui_show"

// Widget holds upstream native components; code fields only read legacy rows.
type Widget struct {
	Title      string      `json:"title"`
	Text       string      `json:"text,omitempty"`
	Replace    string      `json:"replace,omitempty"`
	Fallback   string      `json:"fallback"`
	Components []Component `json:"components,omitempty"`
	HTML       string      `json:"html,omitempty"`
	CSS        string      `json:"css,omitempty"`
	JavaScript string      `json:"javascript,omitempty"`
}

func ShowDefinition() tool.Definition {
	return tool.Definition{Host: true, Spec: tool.Spec{Name: ShowName, Version: 1, Permission: "ui_present", ReplayPolicy: "safe", ResourceHash: "openintelligentui-v4", InputSchema: uidocv1.ShowSchema, ModelInputSchema: uidocv1.ModelShowSchema, OutputBytes: 4096, Description: "Show visuals; read intelligent_ui. text precedes a visual; replace corrects a staged ui_ref. Staged is success."}}
}
func ParseWidget(raw string) (Widget, bool) {
	document, ok := uidocv1.Parse(raw)
	w := Widget{Title: document.Title, Text: document.Text, Replace: document.Replace, Fallback: document.Fallback}
	if document.Components != nil {
		w.Components = make([]Component, len(document.Components))
		for i, c := range document.Components {
			w.Components[i] = Component{Name: c.Name, Props: c.Props}
		}
	}
	return w, ok
}

// Readable keeps the explicitly published paragraph with its non-Web visual alternative.
func (w Widget) Readable() string {
	if w.Text == "" {
		return w.Fallback
	}
	return w.Text + "\n\n" + w.Fallback
}

// StoredWidget never authorizes new input. Legacy snapshots remain readable.
func StoredWidget(raw string) (Widget, bool) {
	if w, ok := ParseWidget(raw); ok {
		return w, true
	}
	var w Widget
	if len(raw) > 128<<10 || json.Unmarshal([]byte(raw), &w) != nil || w.Title == "" || w.Fallback == "" {
		return w, false
	}
	if w.HTML != "" {
		return w, true
	}
	if len(w.Components) == 0 {
		return w, false
	}
	for _, c := range w.Components {
		if !ValidComponent(c) {
			return w, false
		}
	}
	return w, true
}
func WidgetComponents(w Widget, ref, message string, offset int) []Component {
	return bindWidgetComponents(w, ref, message, offset, false)
}

// ManagedWidgetComponents binds Host identities without reinterpreting the
// already accepted component summaries of a managed document.
func ManagedWidgetComponents(w Widget, ref, message string, offset int) []Component {
	return bindWidgetComponents(w, ref, message, offset, true)
}

func bindWidgetComponents(w Widget, ref, message string, offset int, managed bool) []Component {
	if w.HTML != "" {
		return []Component{{Type: "widget", Text: w.Fallback, Title: w.Title, WidgetRef: ref}}
	}
	out := append([]Component(nil), w.Components...)
	for i := range out {
		if out[i].Name != "" {
			out[i].Type = "openintelligentui"
			if out[i].Name == "generateSandboxedUi" {
				out[i].Type = "widget"
				out[i].Title = w.Title
				out[i].WidgetRef = fmt.Sprintf("%s#%d", ref, i)
				out[i].Text = w.Fallback
				continue
			}
			if !managed {
				out[i].Text = nativeSummary(out[i])
			}
			if out[i].Name == "scheduleTime" || out[i].Name == "steward.form" {
				out[i].MessageID = message
				out[i].FormID = Hash([]any{"openintelligentui-response-v1", message, offset + i, out[i]})
			}
		} else {
			if out[i].Type == "table" {
				out[i].Title = w.Title
			}
			if out[i].Type == "form" {
				out[i].MessageID = message
				out[i].FormID = Hash([]any{"form-v2", message, offset + i, out[i]})
			}
		}
	}
	return out
}
