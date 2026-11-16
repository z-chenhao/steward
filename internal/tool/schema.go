package tool

import (
	"encoding/json"
	"io"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"
)

// The registry accepts this deliberately bounded JSON Schema vocabulary. An
// unsupported keyword fails registration, rather than silently skipping a rule.
type schema struct {
	Description string            `json:"description"`
	Title       string            `json:"title"`
	Dialect     string            `json:"$schema"`
	Type        string            `json:"type"`
	Properties  map[string]schema `json:"properties"`
	Required    []string          `json:"required"`
	Additional  *bool             `json:"additionalProperties"`
	Items       *schema           `json:"items"`
	OneOf       []schema          `json:"oneOf"`
	Enum        []any             `json:"enum"`
	Const       json.RawMessage   `json:"const"`
	Minimum     *float64          `json:"minimum"`
	Maximum     *float64          `json:"maximum"`
	MaxLength   *int              `json:"maxLength"`
	MinLength   *int              `json:"minLength"`
	MaxItems    *int              `json:"maxItems"`
	MinItems    *int              `json:"minItems"`
}

// SchemaSupported checks the same bounded vocabulary used at tool admission.
// Plugin registration uses it before accepting executable service contracts.
func SchemaSupported(raw string) bool { _, ok := parseSchema(raw); return ok }

func parseSchema(raw string) (schema, bool) {
	var s schema
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil {
		return s, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return s, false
	}
	var valid func(schema, int) bool
	valid = func(s schema, depth int) bool {
		if depth > 16 {
			return false
		}
		switch s.Type {
		case "", "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return false
		}
		for _, v := range s.Properties {
			if !valid(v, depth+1) {
				return false
			}
		}
		if s.Items != nil && !valid(*s.Items, depth+1) {
			return false
		}
		for _, v := range s.OneOf {
			if !valid(v, depth+1) {
				return false
			}
		}
		return true
	}
	return s, valid(s, 0)
}

// uniqueJSON rejects duplicate object keys at every depth before validation.
func uniqueJSON(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return false
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return false
			}
			seen[key] = true
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	case '[':
		for d.More() {
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, e := d.Token()
	return e == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}
func Validate(schemaJSON, raw string) bool {
	return ValidateBounded(schemaJSON, raw, 65536)
}

// ValidateBounded applies the same strict schema semantics to a caller-owned
// byte budget. Tool arguments retain Validate's 64 KiB limit; bounded service
// results can carry larger documents without enlarging tool admission.
func ValidateBounded(schemaJSON, raw string, maxBytes int) bool {
	if maxBytes <= 0 || len(raw) > maxBytes {
		return false
	}
	s, ok := parseSchema(schemaJSON)
	if !ok {
		return false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	if !uniqueJSON(d, 0) {
		return false
	}
	if _, err := d.Token(); err != io.EOF {
		return false
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return false
	}
	return s.matches(v)
}
func (s schema) matches(v any) bool {
	if len(s.OneOf) > 0 {
		n := 0
		for _, arm := range s.OneOf {
			if arm.matches(v) {
				n++
			}
		}
		if n != 1 {
			return false
		}
	}
	if s.Const != nil {
		var c any
		if json.Unmarshal(s.Const, &c) != nil || !reflect.DeepEqual(c, v) {
			return false
		}
	}
	if s.Enum != nil {
		found := false
		for _, c := range s.Enum {
			if reflect.DeepEqual(c, v) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	switch s.Type {
	case "object":
		o, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range s.Required {
			if _, ok := o[key]; !ok {
				return false
			}
		}
		for key, val := range o {
			child, ok := s.Properties[key]
			if !ok {
				if s.Additional != nil && !*s.Additional {
					return false
				}
			} else if !child.matches(val) {
				return false
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return false
		}
		if s.MaxItems != nil && len(a) > *s.MaxItems || s.MinItems != nil && len(a) < *s.MinItems {
			return false
		}
		if s.Items != nil {
			for _, item := range a {
				if !s.Items.matches(item) {
					return false
				}
			}
		}
	case "string":
		text, ok := v.(string)
		if !ok {
			return false
		}
		n := utf8.RuneCountInString(text)
		if s.MaxLength != nil && n > *s.MaxLength || s.MinLength != nil && n < *s.MinLength {
			return false
		}
	case "number", "integer":
		n, ok := v.(float64)
		if !ok {
			return false
		}
		if s.Type == "integer" && math.Trunc(n) != n {
			return false
		}
		if s.Minimum != nil && n < *s.Minimum || s.Maximum != nil && n > *s.Maximum {
			return false
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return false
		}
	case "null":
		if v != nil {
			return false
		}
	}
	return true
}
