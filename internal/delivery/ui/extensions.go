package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/conversation"
)

// Steward extensions keep the upstream name/props envelope; upstream names and
// props remain intact. These are private Host adapters, not upstream components.
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

func validExtension(c Component) bool {
	switch c.Name {
	case "steward.lineChart":
		var p LineProps
		if json.Unmarshal(c.Props, &p) != nil {
			return false
		}
		seen := map[string]bool{}
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
		if json.Unmarshal(c.Props, &p) != nil {
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
		if json.Unmarshal(c.Props, &p) != nil {
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
		if json.Unmarshal(c.Props, &p) != nil || strings.TrimSpace(p.Title) == "" {
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
	Labels []string `json:"labels"`
	Unit   string   `json:"unit"`
	Series []struct {
		Name   string    `json:"name"`
		Values []float64 `json:"values"`
	} `json:"series"`
}

func extensionSummary(c Component) string {
	var p struct {
		Title, Description, Label string
		Fields                    []FormField
		Data                      []struct {
			Label       string
			Value, X, Y float64
		}
		Items []struct{ Title, Text string }
	}
	if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	lines := []string{p.Title, p.Description, p.Label}
	if c.Name == "steward.lineChart" {
		var chart LineProps
		_ = json.Unmarshal(c.Props, &chart)
		for _, series := range chart.Series {
			for i, value := range series.Values {
				lines = append(lines, fmt.Sprintf("%s / %s: %g %s", series.Name, chart.Labels[i], value, chart.Unit))
			}
		}
	}
	for _, f := range p.Fields {
		lines = append(lines, f.Label)
	}
	for _, d := range p.Data {
		if c.Name == "steward.scatterChart" {
			lines = append(lines, fmt.Sprintf("%s: %g, %g", d.Label, d.X, d.Y))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %g", d.Label, d.Value))
		}
	}
	for _, item := range p.Items {
		lines = append(lines, item.Title, item.Text)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extensionFormAnswer(c Component, values map[string]string) ([]conversation.ContentPart, error) {
	var p FormProps
	if !validNative(c) || json.Unmarshal(c.Props, &p) != nil || len(values) > len(p.Fields) {
		return nil, conversation.ErrInvalid
	}
	known := map[string]bool{}
	for _, f := range p.Fields {
		known[f.Name] = true
	}
	for name := range values {
		if !known[name] {
			return nil, conversation.ErrInvalid
		}
	}
	parts := conversation.Text("Response to: " + p.Title + "\n")
	size := len(parts[0].Text)
	for _, f := range p.Fields {
		value, ok := values[f.Name]
		if !ok && !f.Required {
			ok = true
			if f.Type == "multiselect" {
				value = "[]"
			} else if f.Type == "boolean" {
				value = "false"
			}
		}
		value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
		if !ok || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, conversation.ErrInvalid
		}
		empty := strings.TrimSpace(value) == ""
		if f.Required && empty {
			return nil, conversation.ErrInvalid
		}
		switch f.Type {
		case "number":
			if !empty {
				n, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
					return nil, conversation.ErrInvalid
				}
			}
		case "date":
			if !empty {
				if _, err := time.Parse("2006-01-02", value); err != nil {
					return nil, conversation.ErrInvalid
				}
			}
		case "email":
			if !empty {
				m, err := mail.ParseAddress(value)
				if err != nil || m.Address != value {
					return nil, conversation.ErrInvalid
				}
			}
		case "boolean":
			if value != "true" && value != "false" || f.Required && value != "true" {
				return nil, conversation.ErrInvalid
			}
		case "select":
			if !empty && !formChoice(f, value) {
				return nil, conversation.ErrInvalid
			}
		case "multiselect":
			var selected []string
			if json.Unmarshal([]byte(value), &selected) != nil || selected == nil || len(selected) > len(f.Options) || f.Required && len(selected) == 0 {
				return nil, conversation.ErrInvalid
			}
			seen := map[string]bool{}
			for _, v := range selected {
				if !formChoice(f, v) || seen[v] {
					return nil, conversation.ErrInvalid
				}
				seen[v] = true
			}
			// Option order is the stable identity across browser and native form encoding.
			ordered := []string{}
			for _, o := range f.Options {
				if seen[o.Value] {
					ordered = append(ordered, o.Value)
				}
			}
			raw, _ := json.Marshal(ordered)
			value = string(raw)
		}
		text := f.Label + ": " + value + "\n"
		size += len(text)
		parts = append(parts, conversation.ContentPart{Type: "text", Text: text})
	}
	if size > 16<<10 {
		return nil, conversation.ErrInvalid
	}
	return parts, nil
}
func formChoice(f FormField, value string) bool {
	for _, o := range f.Options {
		if o.Value == value {
			return true
		}
	}
	return false
}
func restoreExtensionForm(c *Component, parts []conversation.ContentPart) error {
	var p FormProps
	if json.Unmarshal(c.Props, &p) != nil || len(parts) != len(p.Fields)+1 {
		return conversation.ErrConflict
	}
	values := map[string]string{}
	for i, f := range p.Fields {
		v, ok := strings.CutPrefix(parts[i+1].Text, f.Label+": ")
		v, end := strings.CutSuffix(v, "\n")
		if !ok || !end {
			return conversation.ErrConflict
		}
		values[f.Name] = v
	}
	expected, err := extensionFormAnswer(*c, values)
	if err != nil || Hash(expected) != Hash(parts) {
		return conversation.ErrConflict
	}
	raw, _ := json.Marshal(values)
	c.Response = string(raw)
	c.Submitted = true
	return nil
}
func extensionFormHTML(c Component) string {
	var p FormProps
	if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	esc := template.HTMLEscapeString
	values := map[string]string{}
	_ = json.Unmarshal([]byte(c.Response), &values)
	var b strings.Builder
	fmt.Fprintf(&b, `<form class="native-input-form" method="post" action="/ui/form" data-form-id="%s"><h3>%s</h3><input type="hidden" name="message_id" value="%s"><input type="hidden" name="form_id" value="%s"><fieldset`, esc(c.FormID), esc(p.Title), esc(c.MessageID), esc(c.FormID))
	if c.Submitted || c.Disabled {
		b.WriteString(` disabled`)
	}
	b.WriteString(`>`)
	for _, f := range p.Fields {
		value := values[f.Name]
		required := ""
		if f.Required {
			required = " required"
		}
		name := "field." + f.Name
		id := c.FormID + "-" + f.Name
		fmt.Fprintf(&b, `<div class="native-form-field"><label for="%s">%s</label>`, esc(id), esc(f.Label))
		switch f.Type {
		case "textarea":
			fmt.Fprintf(&b, `<textarea id="%s" name="%s" maxlength="4096"%s>%s</textarea>`, esc(id), esc(name), required, esc(value))
		case "select", "multiselect", "boolean":
			multi := ""
			if f.Type == "multiselect" {
				name += "[]"
				multi = " multiple"
			}
			fmt.Fprintf(&b, `<select id="%s" name="%s"%s%s>`, esc(id), esc(name), multi, required)
			options := f.Options
			if f.Type == "boolean" {
				options = []FormOption{{"false", "No"}, {"true", "Yes"}}
			}
			if f.Type == "select" {
				b.WriteString(`<option value=""></option>`)
			}
			var selected []string
			_ = json.Unmarshal([]byte(value), &selected)
			for _, o := range options {
				sel := ""
				if o.Value == value {
					sel = " selected"
				}
				for _, v := range selected {
					if v == o.Value {
						sel = " selected"
					}
				}
				fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, esc(o.Value), sel, esc(o.Label))
			}
			b.WriteString(`</select>`)
		default:
			typ := f.Type
			if typ == "text" || typ == "email" || typ == "date" || typ == "number" {
				fmt.Fprintf(&b, `<input id="%s" type="%s" name="%s" value="%s" maxlength="4096"%s`, esc(id), typ, esc(name), esc(value), required)
				if f.Min != nil {
					fmt.Fprintf(&b, ` min="%g"`, *f.Min)
				}
				if f.Max != nil {
					fmt.Fprintf(&b, ` max="%g"`, *f.Max)
				}
				if typ == "number" {
					b.WriteString(` step="any"`)
				}
				b.WriteString(`>`)
			}
		}
		b.WriteString(`</div>`)
	}
	label := p.SubmitLabel
	if c.Submitted {
		label = "Sent"
	}
	if label == "" {
		label = "Send"
	}
	fmt.Fprintf(&b, `<button type="submit">%s</button></fieldset></form>`, esc(label))
	return b.String()
}
