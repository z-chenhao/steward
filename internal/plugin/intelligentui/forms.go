package intelligentui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

var errForm = errors.New("invalid UI form")

func FormAnswer(f contract.FormFacts) contract.FormResult {
	out := contract.FormResult{Parts: []string{}}
	c := Component{Name: f.Name, Props: f.Props}
	if c.Name != "steward.form" && c.Name != "scheduleTime" {
		out.ErrorCode = "INVALID_ARGUMENT"
		return out
	}
	var parts []string
	var err error
	if c.Name == "steward.form" {
		parts, err = extensionFormAnswer(c, f.Values)
	} else {
		parts, err = meetingAnswer(c, f.Values)
	}
	if err != nil {
		out.ErrorCode = "INVALID_ARGUMENT"
		return out
	}
	response, err := restoreFormAnswer(c, parts)
	if err != nil {
		out.ErrorCode = "CONFLICT"
		return out
	}
	out.Parts = parts
	out.Response = response
	return out
}

func restoreFormAnswer(c Component, parts []string) (string, error) {
	if c.Name == "scheduleTime" {
		var p nativeProps
		if json.Unmarshal(c.Props, &p) != nil {
			return "", errForm
		}
		choices := []string{"decline"}
		for i := range p.TimeSlots {
			choices = append(choices, strconv.Itoa(i))
		}
		for _, choice := range choices {
			expected, err := meetingAnswer(c, map[string]string{"choice": choice})
			if err == nil && reflect.DeepEqual(expected, parts) {
				return choice, nil
			}
		}
		return "", errForm
	}
	var p FormProps
	if json.Unmarshal(c.Props, &p) != nil || len(parts) != len(p.Fields)+1 {
		return "", errForm
	}
	values := map[string]string{}
	for i, f := range p.Fields {
		value, ok := strings.CutPrefix(parts[i+1], f.Label+": ")
		value, end := strings.CutSuffix(value, "\n")
		if !ok || !end {
			return "", errForm
		}
		values[f.Name] = value
	}
	expected, err := extensionFormAnswer(c, values)
	if err != nil || !reflect.DeepEqual(expected, parts) {
		return "", errForm
	}
	raw, _ := json.Marshal(values)
	return string(raw), nil
}

func extensionFormAnswer(c Component, values map[string]string) ([]string, error) {
	var p FormProps
	if !validNative(c) || json.Unmarshal(c.Props, &p) != nil || len(values) > len(p.Fields) {
		return nil, errForm
	}
	known := map[string]bool{}
	for _, f := range p.Fields {
		known[f.Name] = true
	}
	for name := range values {
		if !known[name] {
			return nil, errForm
		}
	}
	parts := []string{"Response to: " + p.Title + "\n"}
	size := len(parts[0])
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
			return nil, errForm
		}
		empty := strings.TrimSpace(value) == ""
		if f.Required && empty {
			return nil, errForm
		}
		switch f.Type {
		case "number":
			if !empty {
				n, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
					return nil, errForm
				}
			}
		case "date":
			if !empty {
				if _, err := time.Parse("2006-01-02", value); err != nil {
					return nil, errForm
				}
			}
		case "email":
			if !empty {
				m, err := mail.ParseAddress(value)
				if err != nil || m.Address != value {
					return nil, errForm
				}
			}
		case "boolean":
			if value != "true" && value != "false" || f.Required && value != "true" {
				return nil, errForm
			}
		case "select":
			if !empty && !formChoice(f, value) {
				return nil, errForm
			}
		case "multiselect":
			var selected []string
			if json.Unmarshal([]byte(value), &selected) != nil || selected == nil || len(selected) > len(f.Options) || f.Required && len(selected) == 0 {
				return nil, errForm
			}
			seen := map[string]bool{}
			for _, v := range selected {
				if !formChoice(f, v) || seen[v] {
					return nil, errForm
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
		parts = append(parts, text)
	}
	if size > 16<<10 {
		return nil, errForm
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

func meetingAnswer(c Component, values map[string]string) ([]string, error) {
	if !validNative(c) || len(values) != 1 {
		return nil, errForm
	}
	value, ok := values["choice"]
	if !ok {
		return nil, errForm
	}
	var p nativeProps
	_ = json.Unmarshal(c.Props, &p)
	if value == "decline" {
		return []string{"Meeting time preference for " + p.Reason + ": none of the proposed times work. Please suggest alternatives."}, nil
	}
	i, err := strconv.Atoi(value)
	if err != nil || i < 0 || i >= len(p.TimeSlots) || strconv.Itoa(i) != value {
		return nil, errForm
	}
	slot := p.TimeSlots[i]
	return []string{fmt.Sprintf("Meeting time preference for %s: %s at %s (%g min). This selects a preferred time; it does not book a meeting.", p.Reason, slot.Date, slot.Time, p.Duration)}, nil
}
