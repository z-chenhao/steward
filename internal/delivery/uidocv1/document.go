// Package uidocv1 defines the version-one Intelligent UI wire grammar shared
// by the historical Host reader and the independently compiled plugin.
package uidocv1

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
)

var fieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func ValidFieldName(name string) bool {
	return fieldName.MatchString(name)
}

// Component contains only the existing presentation input fields. Publication,
// approval and interaction identities belong to Host, never this plugin model.
type Component struct {
	Name  string          `json:"name,omitempty"`
	Props json.RawMessage `json:"props,omitempty"`
}

type Document struct {
	Title      string      `json:"title"`
	Text       string      `json:"text,omitempty"`
	Replace    string      `json:"replace,omitempty"`
	Fallback   string      `json:"fallback"`
	Components []Component `json:"components,omitempty"`
}

const nativeSchema = `{"oneOf":[{"type":"object","properties":{"name":{"type":"string","const":"Table"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"columns":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","maxLength":200}},"rows":{"type":"array","maxItems":200,"items":{"type":"array","maxItems":12,"items":{"type":"string","maxLength":2000}}},"source":{"type":"string","maxLength":1000}},"required":["title","columns","rows","source"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Exact values, records and simple side-by-side comparisons. For trends, mechanisms or adjustable assumptions choose a chart or generateSandboxedUi instead."},{"type":"object","properties":{"name":{"type":"string","const":"barChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Compare magnitudes across a few named categories. Description gives units and real source or labels illustrative data."},{"type":"object","properties":{"name":{"type":"string","const":"pieChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Parts of one meaningful positive total, with few categories. Use barChart for close comparisons."},{"type":"object","properties":{"name":{"type":"string","const":"plan_visualization"},"props":{"type":"object","properties":{"approach":{"type":"string","maxLength":8192},"technology":{"type":"string","maxLength":300},"key_elements":{"type":"array","maxItems":32,"items":{"type":"string","maxLength":1000}}},"required":["approach","technology","key_elements"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"An optional visualization plan, not an answer or evidence of execution progress. Do not call just to announce a visual."},{"type":"object","properties":{"name":{"type":"string","const":"scheduleTime"},"props":{"type":"object","properties":{"reasonForScheduling":{"type":"string","maxLength":300},"meetingDuration":{"type":"number","minimum":0,"maximum":1440},"title":{"type":"string","maxLength":300},"timeSlots":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"object","properties":{"date":{"type":"string","maxLength":200,"minLength":1},"time":{"type":"string","maxLength":200,"minLength":1},"duration":{"type":"string","maxLength":100}},"required":["date","time"],"additionalProperties":false}}},"required":["reasonForScheduling","meetingDuration","timeSlots"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Collect a preferred time from explicit real candidate dates. This never books a meeting."},{"type":"object","properties":{"name":{"type":"string","const":"generateSandboxedUi"},"props":{"type":"object","properties":{"initialHeight":{"type":"number","minimum":50,"maximum":4000},"placeholderMessages":{"type":"array","maxItems":4,"items":{"type":"string","maxLength":200},"description":"Optional short loading messages. Use [] when unnecessary."},"css":{"type":"string","maxLength":12000,"description":"Widget-scoped layout only. Use injected theme tokens and shared controls; keep labels readable at narrow widths."},"html":{"type":"string","maxLength":28000,"minLength":1,"description":"Body fragment with one named root, meaningful labels and a responsive layout. Shared theme and controls are injected. Do not include imports, remote scripts or an application shell."},"jsFunctions":{"type":"string","maxLength":12000,"description":"Named classic-script functions; use local d3, THREE or native SVG/Canvas. No top-level await. Initialize after markup arrives."},"jsExpressions":{"description":"Initialization statements, run in order after jsFunctions. Use [] for a static visual. Exactly an empty string is also accepted for compatibility.","oneOf":[{"type":"array","maxItems":32,"items":{"type":"string","maxLength":2000}},{"type":"string","const":""}]}},"required":["initialHeight","placeholderMessages","css","html","jsFunctions","jsExpressions"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Custom diagrams, multi-series charts, calculators, maps from supplied geography, and interactive or 3D explanations. Local d3, THREE (including THREE.SVGRenderer), and GEO.countries/GEO.source globals are available. No imports or network. Follow the ordered fields; make controls change meaningful results."},{"type":"object","properties":{"name":{"type":"string","const":"steward.lineChart"},"props":{"oneOf":[{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","maxItems":200,"items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"value":{"type":"number","minimum":-1000000000000000,"maximum":1000000000000000}},"required":["label","value"],"additionalProperties":false}}},"required":["title","description","data"],"additionalProperties":false},{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"unit":{"type":"string","maxLength":64},"labels":{"type":"array","maxItems":200,"items":{"type":"string","maxLength":200}},"series":{"type":"array","minItems":1,"maxItems":6,"items":{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":200},"values":{"type":"array","maxItems":200,"items":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}}},"required":["name","values"],"additionalProperties":false}},"xValues":{"type":"array","maxItems":200,"items":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}}},"required":["title","description","labels","series"],"additionalProperties":false}]}},"required":["name","props"],"additionalProperties":false,"description":"Shared series on ordered categories, or explicit strictly increasing xValues for a proportional numeric axis. Each values/xValues array matches labels. Use the sandbox for other geometry."},{"type":"object","properties":{"name":{"type":"string","const":"steward.scatterChart"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"description":{"type":"string","maxLength":2000},"data":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string","maxLength":200},"x":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0},"y":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}},"required":["label","x","y"],"additionalProperties":false},"maxItems":200,"minItems":0}},"required":["title","description","data"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"The relationship between two numeric variables, with named observations."},{"type":"object","properties":{"name":{"type":"string","const":"steward.form"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"fields":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string","maxLength":64},"label":{"type":"string","maxLength":256},"type":{"type":"string","enum":["text","textarea","number","date","email","select","multiselect","boolean"]},"required":{"type":"boolean"},"options":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string","maxLength":200},"label":{"type":"string","maxLength":200}},"required":["value","label"],"additionalProperties":false},"maxItems":32,"minItems":0},"min":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0},"max":{"type":"number","minimum":-1000000000000000.0,"maximum":1000000000000000.0}},"required":["name","label","type"],"additionalProperties":false},"maxItems":16,"minItems":1},"submitLabel":{"type":"string","maxLength":100}},"required":["title","fields"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Collect structured user inputs with typed validation. It does not approve or execute external actions."},{"type":"object","properties":{"name":{"type":"string","const":"steward.cards"},"props":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"items":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string","maxLength":300},"text":{"type":"string","maxLength":2000},"image_ref":{"type":"string","maxLength":256},"url":{"type":"string","maxLength":2048},"label":{"type":"string","maxLength":100}},"required":["title"],"additionalProperties":false},"maxItems":16,"minItems":1}},"required":["items"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Compare a few distinct choices, with optional authorized artifact images and HTTPS links."},{"type":"object","properties":{"name":{"type":"string","const":"steward.action"},"props":{"type":"object","properties":{"id":{"type":"string","maxLength":64},"label":{"type":"string","maxLength":100},"tool":{"type":"string","maxLength":200},"arguments":{"type":"object"},"bindings":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"argument":{"type":"string","minLength":1,"maxLength":64},"component":{"type":"integer","minimum":0,"maximum":15},"path":{"type":"string","minLength":1,"maxLength":512}},"required":["argument","component","path"],"additionalProperties":false}}},"required":["id","label","tool","arguments"],"additionalProperties":false}},"required":["name","props"],"additionalProperties":false,"description":"Offer an exact authorized tool request through the Host; existing approval and execution rules remain in force."}]}`

const ShowSchema = `{"type":"object","properties":{"text":{"type":"string","maxLength":8192,"description":"Optional user-facing Markdown immediately before this visual. Use for an explanation that belongs here in the answer, not work narration. Omit when the visual stands alone; final reply follows all staged sections."},"replace":{"type":"string","minLength":1,"maxLength":256,"description":"Optional exact ui_ref from an earlier successful show in this same pending reply. Replaces that unbound presentation only; omit to add a distinct visual. Delivered messages cannot be replaced."},"title":{"type":"string","minLength":1,"maxLength":200,"description":"Short content title; the Host displays it once."},"fallback":{"type":"string","minLength":1,"maxLength":8192,"description":"Complete readable alternative for non-Web delivery and render failure. Do not repeat it in the final reply."},"components":{"type":"array","minItems":1,"maxItems":16,"items":` + nativeSchema + `,"description":"Choose the smallest useful composition for this task. Prefer one main visual. Native components for exact-fit tasks; generateSandboxedUi for richer explanations or exploration."}},"required":["title","fallback","components"],"additionalProperties":false}`

func Parse(raw string) (Document, bool) {
	var w Document
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
		if !ValidComponent(c.Name, c.Props) {
			return w, false
		}
		if c.Name == "scheduleTime" || c.Name == "steward.form" {
			meetings++
		}
	}
	return w, meetings <= 4
}

type SandboxedUI struct {
	InitialHeight       float64  `json:"initialHeight"`
	PlaceholderMessages []string `json:"placeholderMessages"`
	CSS                 string   `json:"css"`
	HTML                string   `json:"html"`
	JSFunctions         string   `json:"jsFunctions"`
	JSExpressions       []string `json:"jsExpressions"`
}

type NativeProps struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Columns     []string   `json:"columns"`
	Rows        [][]string `json:"rows"`
	Source      string     `json:"source"`
	Data        []struct {
		Label string  `json:"label"`
		Value float64 `json:"value"`
	} `json:"data"`
	Approach    string   `json:"approach"`
	Technology  string   `json:"technology"`
	KeyElements []string `json:"key_elements"`
	Reason      string   `json:"reasonForScheduling"`
	Duration    float64  `json:"meetingDuration"`
	TimeSlots   []struct {
		Date     string `json:"date"`
		Time     string `json:"time"`
		Duration string `json:"duration"`
	} `json:"timeSlots"`
}

func ValidComponent(name string, props json.RawMessage) bool {
	raw, err := json.Marshal(struct {
		Name  string          `json:"name"`
		Props json.RawMessage `json:"props"`
	}{name, props})
	if err != nil || !tool.Validate(nativeSchema, string(raw)) {
		return false
	}
	var p NativeProps
	if json.Unmarshal(props, &p) != nil {
		return false
	}
	if strings.HasPrefix(name, "steward.") {
		return validExtension(name, props)
	}
	switch name {
	case "generateSandboxedUi":
		var visual SandboxedUI
		if json.Unmarshal(props, &visual) != nil || strings.TrimSpace(visual.HTML) == "" {
			return false
		}
	case "Table":
		for _, r := range p.Rows {
			if len(r) != len(p.Columns) {
				return false
			}
		}
	case "pieChart":
		total := 0.0
		for _, d := range p.Data {
			if d.Value < 0 {
				return false
			}
			total += d.Value
		}
		if len(p.Data) > 0 && total <= 0 {
			return false
		}
	case "scheduleTime":
		if p.Duration <= 0 || strings.TrimSpace(p.Reason) == "" {
			return false
		}
		seen := map[string]bool{}
		for _, slot := range p.TimeSlots {
			key := slot.Date + "\x00" + slot.Time
			if strings.TrimSpace(slot.Date) == "" || strings.TrimSpace(slot.Time) == "" || seen[key] {
				return false
			}
			seen[key] = true
		}
	}
	return true
}

type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type FormField struct {
	Name     string       `json:"name"`
	Label    string       `json:"label"`
	Type     string       `json:"type"`
	Required bool         `json:"required,omitempty"`
	Options  []FormOption `json:"options,omitempty"`
	Min      *float64     `json:"min,omitempty"`
	Max      *float64     `json:"max,omitempty"`
}
type FormProps struct {
	Title       string      `json:"title"`
	Fields      []FormField `json:"fields"`
	SubmitLabel string      `json:"submitLabel,omitempty"`
}
type ActionBinding struct {
	Argument  string `json:"argument"`
	Component int    `json:"component"`
	Path      string `json:"path"`
}
type ActionProps struct {
	ID        string          `json:"id"`
	Label     string          `json:"label"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Bindings  []ActionBinding `json:"bindings,omitempty"`
}

func validExtension(name string, props json.RawMessage) bool {
	switch name {
	case "steward.lineChart":
		var p LineProps
		if json.Unmarshal(props, &p) != nil {
			return false
		}
		seen := map[string]bool{}
		if p.XValues != nil {
			if len(p.XValues) != len(p.Labels) {
				return false
			}
			for i, x := range p.XValues {
				if i > 0 && x <= p.XValues[i-1] {
					return false
				}
			}
		}
		for _, series := range p.Series {
			if strings.TrimSpace(series.Name) == "" || seen[series.Name] || len(series.Values) != len(p.Labels) {
				return false
			}
			seen[series.Name] = true
		}
		return true
	case "steward.scatterChart":
		return true // Full bounded schema was already validated.
	case "steward.action":
		var p ActionProps
		if json.Unmarshal(props, &p) != nil {
			return false
		}
		seen := map[string]bool{}
		for _, binding := range p.Bindings {
			if !fieldName.MatchString(binding.Argument) || seen[binding.Argument] || !strings.HasPrefix(binding.Path, "/") {
				return false
			}
			seen[binding.Argument] = true
			for _, segment := range strings.Split(strings.TrimPrefix(binding.Path, "/"), "/") {
				for i := 0; i < len(segment); i++ {
					if segment[i] == '~' {
						if i+1 >= len(segment) || segment[i+1] != '0' && segment[i+1] != '1' {
							return false
						}
						i++
					}
				}
			}
		}
		return fieldName.MatchString(p.ID) && strings.TrimSpace(p.Label) != "" && len(p.Arguments) <= 8192
	case "steward.cards":
		var p struct {
			Items []struct {
				Title, Text string
				ImageRef    string `json:"image_ref"`
				URL         string `json:"url"`
			} `json:"items"`
		}
		if json.Unmarshal(props, &p) != nil {
			return false
		}
		for _, item := range p.Items {
			if strings.TrimSpace(item.Title) == "" {
				return false
			}
			if item.ImageRef != "" && (strings.Contains(item.ImageRef, ":") && !strings.HasPrefix(item.ImageRef, "artifact:") || strings.ContainsAny(item.ImageRef, "/\\ \r\n\x00")) {
				return false
			}
			if item.URL != "" {
				u, err := url.Parse(item.URL)
				if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
					return false
				}
			}
		}
		return true
	case "steward.form":
		var p FormProps
		if json.Unmarshal(props, &p) != nil || strings.TrimSpace(p.Title) == "" {
			return false
		}
		seen := map[string]bool{}
		for _, f := range p.Fields {
			if !fieldName.MatchString(f.Name) || seen[f.Name] || strings.TrimSpace(f.Label) == "" || f.Min != nil && f.Max != nil && *f.Min > *f.Max {
				return false
			}
			seen[f.Name] = true
			selection := f.Type == "select" || f.Type == "multiselect"
			if selection && len(f.Options) == 0 || !selection && len(f.Options) > 0 || f.Type != "number" && (f.Min != nil || f.Max != nil) {
				return false
			}
			values := map[string]bool{}
			for _, o := range f.Options {
				if o.Value == "" || o.Label == "" || values[o.Value] {
					return false
				}
				values[o.Value] = true
			}
		}
		return true
	}
	return false
}

type LineProps struct {
	Labels  []string  `json:"labels"`
	XValues []float64 `json:"xValues"`
	Unit    string    `json:"unit"`
	Series  []struct {
		Name   string    `json:"name"`
		Values []float64 `json:"values"`
	} `json:"series"`
}
