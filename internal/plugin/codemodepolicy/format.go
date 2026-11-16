package codemodepolicy

import (
	"encoding/json"
	"sort"
	"strings"
)

// Types describe shape; complete validation constraints remain in InputSchema.
func inputType(value any) string {
	s, ok := value.(map[string]any)
	if !ok {
		return "unknown"
	}
	if constant, ok := s["const"]; ok {
		raw, _ := json.Marshal(constant)
		return string(raw)
	}
	if values, ok := s["enum"].([]any); ok && len(values) > 0 {
		types := []string{}
		for _, v := range values {
			raw, _ := json.Marshal(v)
			types = append(types, string(raw))
		}
		return strings.Join(types, " | ")
	}
	if values, ok := s["oneOf"].([]any); ok && len(values) > 0 {
		types := []string{}
		for _, v := range values {
			types = append(types, inputType(v))
		}
		return strings.Join(types, " | ")
	}
	switch s["type"] {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return "ReadonlyArray<" + inputType(s["items"]) + ">"
	case "object":
		properties, _ := s["properties"].(map[string]any)
		required := map[string]bool{}
		if values, ok := s["required"].([]any); ok {
			for _, v := range values {
				if name, ok := v.(string); ok {
					required[name] = true
				}
			}
		}
		keys := []string{}
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fields := []string{}
		for _, key := range keys {
			name, _ := json.Marshal(key)
			optional := "?"
			if required[key] {
				optional = ""
			}
			fields = append(fields, string(name)+optional+": "+inputType(properties[key]))
		}
		if s["additionalProperties"] != false {
			fields = append(fields, "[key: string]: unknown")
		}
		return "{ " + strings.Join(fields, "; ") + " }"
	}
	return "unknown"
}
