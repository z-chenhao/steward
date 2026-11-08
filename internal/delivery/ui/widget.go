package ui

import (
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
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

const NativeSchema = `{"oneOf":[{"type":"object","properties":{"name":{"type":"string","const":"Table"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"columns":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","maxLength":200}},"rows":{"type":"array","maxItems":200,"items":{"type":"array","maxItems":12,"items":{"type":"string","maxLength":2000}}},"source":{"type":"string","maxLength":1000}},"required":["title","columns","rows","source"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Exact values, records and simple side-by-side comparisons. For trends, mechanisms or adjustable assumptions choose a chart or generateSandboxedUi instead."},{"type":"object","properties":{"name":{"type":"string","const":"barChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Compare magnitudes across a few named categories. Description gives units and real source or labels illustrative data."},{"type":"object","properties":{"name":{"type":"string","const":"pieChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Parts of one meaningful positive total, with few categories. Use barChart for close comparisons."},{"type":"object","properties":{"name":{"type":"string","const":"plan_visualization"},"props":{"type":"object","properties":{"approach":{"type":"string","maxLength":8192},"technology":{"type":"string","maxLength":300},"key_elements":{"type":"array","maxItems":32,"items":{"type":"string","maxLength":1000}}},"required":["approach","technology","key_elements"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"An optional visualization plan, not an answer or evidence of execution progress. Do not call just to announce a visual."},{"type":"object","properties":{"name":{"type":"string","const":"scheduleTime"},"props":{"type":"object","properties":{"reasonForScheduling":{"type":"string","maxLength":300},"meetingDuration":{"type":"number","minimum":0,"maximum":1440},"title":{"type":"string","maxLength":300},"timeSlots":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"object","properties":{"date":{"type":"string","maxLength":200,"minLength":1},"time":{"type":"string","maxLength":200,"minLength":1},"duration":{"type":"string","maxLength":100}},"required":["date","time"],"additionalProperties":false}}},"required":["reasonForScheduling","meetingDuration","timeSlots"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Collect a preferred time from explicit real candidate dates. This never books a meeting."},{"type":"object","properties":{"name":{"type":"string","const":"generateSandboxedUi"},"props":{"type":"object","properties":{"initialHeight":{"type":"number","minimum":50,"maximum":4000},"placeholderMessages":{"type":"array","maxItems":4,"items":{"type":"string","maxLength":200},"description":"Optional short loading messages. Use [] when unnecessary."},"css":{"type":"string","maxLength":12000,"description":"Widget-scoped layout only. Use injected theme tokens and shared controls; keep labels readable at narrow widths."},"html":{"type":"string","maxLength":28000,"minLength":1,"description":"Body fragment with one named root, meaningful labels and a responsive layout. Shared theme and controls are injected. Do not include imports, remote scripts or an application shell."},"jsFunctions":{"type":"string","maxLength":12000,"description":"Named classic-script functions; use local d3, THREE or native SVG/Canvas. No top-level await. Initialize after markup arrives."},"jsExpressions":{"description":"Initialization statements, run in order after jsFunctions. Use [] for a static visual. Exactly an empty string is also accepted for compatibility.","oneOf":[{"type":"array","maxItems":32,"items":{"type":"string","maxLength":2000}},{"type":"string","const":""}]}},"required":["initialHeight","placeholderMessages","css","html","jsFunctions","jsExpressions"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Custom diagrams, multi-series charts, calculators, maps from supplied geography, and interactive or 3D explanations. Local d3, THREE (including THREE.SVGRenderer), and GEO.countries/GEO.source globals are available. No imports or network. Follow the ordered fields; make controls change meaningful results."},{"type":"object","properties":{"name":{"type":"string","const":"steward.lineChart"},"props":{"oneOf":[{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false},{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"unit":{"type":"string","maxLength":64},"labels":{"type":"array","maxItems":200,"items":{"type":"string","maxLength":200}},"series":{"type":"array","minItems":1,"maxItems":6,"items":{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":200},"values":{"type":"array","maxItems":200,"items":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}}},"required":["name","values"],"additionalProperties":false}},"xValues":{"type":"array","maxItems":200,"items":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}}},"required":["title","description","labels","series"],"additionalProperties":false}]}},"required":["name","props"],"additionalProperties":false,"description":"Shared series on ordered categories, or explicit strictly increasing xValues for a proportional numeric axis. Each values/xValues array matches labels. Use the sandbox for other geometry."},{"type":"object","properties":{"name":{"type":"string","const":"steward.scatterChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"x":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0},"y":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}},"required":["label","x","y"],"additionalProperties":false},"maxItems":200,"minItems":0}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"The relationship between two numeric variables, with named observations."},{"type":"object","properties":{"name":{"type":"string","const":"steward.form"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"fields":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string","maxLength":64},"label":{"type":"string","maxLength":256},"type":{"type":"string","enum":["text","textarea","number","date","email","select","multiselect","boolean"]},"required":{"type":"boolean"},"options":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string","maxLength":200},"label":{"type":"string","maxLength":200}},"required":["value","label"],"additionalProperties":false},"maxItems":32,"minItems":0},"min":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0},"max":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}},"required":["name","label","type"],"additionalProperties":false},"maxItems":16,"minItems":1},"submitLabel":{"type":"string","maxLength":100}},"required":["title","fields"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Collect structured user inputs with typed validation. It does not approve or execute external actions."},{"type":"object","properties":{"name":{"type":"string","const":"steward.cards"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"items":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"text":{"type":"string","maxLength":2000},"image_ref":{"type":"string","maxLength":256},"url":{"type":"string","maxLength":2048},"label":{"type":"string","maxLength":100}},"required":["title"],"additionalProperties":false},"maxItems":16,"minItems":1}},"required":["items"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Compare a few distinct choices, with optional authorized artifact images and HTTPS links."},{"type":"object","properties":{"name":{"type":"string","const":"steward.action"},"props":{"type":"object","properties":{"id":{"type":"string","maxLength":64},"label":{"type":"string","maxLength":100},"tool":{"type":"string","maxLength":200},"arguments":{"type":"object"},"bindings":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"argument":{"type":"string","minLength":1,"maxLength":64},"component":{"type":"integer","minimum":0,"maximum":15},"path":{"type":"string","minLength":1,"maxLength":512}},"required":["argument","component","path"],"additionalProperties":false}}},"required":["id","label","tool","arguments"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Offer an exact authorized tool request through the Host; existing approval and execution rules remain in force."}]}`
const ShowSchema = `{"type":"object","properties":{"text":{"type":"string","maxLength":8192,"description":"Optional user-facing Markdown immediately before this visual. Use for an explanation that belongs here in the answer, not work narration. Omit when the visual stands alone; final reply follows all staged sections."},"replace":{"type":"string","minLength":1,"maxLength":256,"description":"Optional exact ui_ref from an earlier successful show in this same pending reply. Replaces that unbound presentation only; omit to add a distinct visual. Delivered messages cannot be replaced."},"title":{"type":"string","minLength":1,"maxLength":200,"description":"Short content title; the Host displays it once."},"fallback":{"type":"string","minLength":1,"maxLength":8192,"description":"Complete readable alternative for non-Web delivery and render failure. Do not repeat it in the final reply."},"components":{"type":"array","minItems":1,"maxItems":16,"items":` + NativeSchema + `,"description":"Choose the smallest useful composition for this task. Prefer one main visual. Native components for exact-fit tasks; generateSandboxedUi for richer explanations or exploration."}},"required":["title","fallback","components"],"additionalProperties":false}`

func ShowDefinition() tool.Definition {
	return tool.Definition{Host: true, Spec: tool.Spec{Name: ShowName, Version: 1, Permission: "ui_present", ReplayPolicy: "safe", ResourceHash: "openintelligentui-v4", InputSchema: ShowSchema, ModelInputSchema: modelShowSchema, OutputBytes: 4096, Description: "Show a grounded visual with fallback. Read intelligent_ui for props and local libraries. Optional text goes before this visual; replace corrects a staged ui_ref. A staged receipt is success; finish unless adding a distinct section."}}
}
func ParseWidget(raw string) (Widget, bool) {
	var w Widget
	if len(raw) > 128<<10 || !tool.Validate(ShowSchema, raw) || json.Unmarshal([]byte(raw), &w) != nil || strings.TrimSpace(w.Title) == "" || strings.TrimSpace(w.Fallback) == "" {
		return w, false
	}
	meetings := 0
	actionIDs := map[string]bool{}
	for index, c := range w.Components {
		// An empty script string contains no executable statements. Normalize
		// only this shorthand before applying the unchanged catalog validation.
		if c.Name == "generateSandboxedUi" {
			var props map[string]json.RawMessage
			if json.Unmarshal(c.Props, &props) == nil && string(props["jsExpressions"]) == `""` {
				props["jsExpressions"] = json.RawMessage(`[]`)
				c.Props, _ = json.Marshal(props)
				w.Components[index] = c
			}
		}
		if c.Name == "steward.action" {
			var action ActionProps
			if json.Unmarshal(c.Props, &action) != nil || actionIDs[action.ID] {
				return w, false
			}
			actionIDs[action.ID] = true
			for _, binding := range action.Bindings {
				if binding.Component >= len(w.Components) || binding.Component == index {
					return w, false
				}
			}
		}
		if !validNative(c) {
			return w, false
		}
		if c.Name == "scheduleTime" || c.Name == "steward.form" {
			meetings++
		}
	}
	return w, meetings <= 4
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
		if !valid(c) {
			return w, false
		}
	}
	return w, true
}
func WidgetComponents(w Widget, ref, message string, offset int) []Component {
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
			out[i].Text = nativeSummary(out[i])
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
