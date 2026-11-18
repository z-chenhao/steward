package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/uidocv1"
)

type FormOption = uidocv1.FormOption
type FormField = uidocv1.FormField
type FormProps = uidocv1.FormProps
type ActionProps = uidocv1.ActionProps

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
		var chart uidocv1.LineProps
		_ = json.Unmarshal(c.Props, &chart)
		for _, series := range chart.Series {
			for i, value := range series.Values {
				label := chart.Labels[i]
				if chart.XValues != nil {
					label = fmt.Sprintf("%s (x=%g)", label, chart.XValues[i])
				}
				lines = append(lines, fmt.Sprintf("%s / %s: %g %s", series.Name, label, value, chart.Unit))
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
	if c.ViewRef != "" && c.FormView == nil {
		return `<p class="semantic">` + template.HTMLEscapeString(c.Text) + `</p>`
	}
	var p FormProps
	values := map[string]string{}
	if c.FormView != nil {
		p = c.FormView.Props
		for name, value := range c.FormView.Defaults {
			values[name] = value
		}
	} else if json.Unmarshal(c.Props, &p) != nil {
		return ""
	}
	esc := template.HTMLEscapeString
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
				options = []FormOption{{Value: "false", Label: "No"}, {Value: "true", Label: "Yes"}}
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
