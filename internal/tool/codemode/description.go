package codemode

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
)

type ToolDescription struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  string `json:"inputSchema"`
	Declaration  string `json:"declaration"`
	Instructions string `json:"instructions"`
}

func Describe(s tool.Spec) ToolDescription {
	var input any
	_ = json.Unmarshal([]byte(s.InputSchema), &input)
	return DescribeInputType(s, typeScript(input), "")
}

// DescribeInputType keeps identity, original schema and result shape Host-owned.
// Only argument-type documentation and an appended method hint are replaceable.
func DescribeInputType(s tool.Spec, inputType, guidance string) ToolDescription {
	name, _ := json.Marshal(s.Name)
	output := "unknown"
	if s.Name == "builtin__agent_run" {
		output = "{ readonly text: string; readonly artifactRefs: readonly string[] }"
	}
	instructions := s.Description
	if guidance != "" {
		instructions += "\n" + guidance
	}
	return ToolDescription{Name: s.Name, Description: s.Description, InputSchema: s.InputSchema, Declaration: "tools[" + string(name) + "](args: " + inputType + "): Promise<" + output + ">", Instructions: instructions}
}

func NamespaceMatches(toolName, namespace string) bool {
	if name, ok := strings.CutPrefix(toolName, "builtin__"); ok {
		namespace = strings.TrimPrefix(namespace, "builtin__")
		return namespace != "" && !strings.Contains(namespace, "__") && !strings.Contains(name, "__") && strings.HasPrefix(name, namespace+"_")
	}
	parts := strings.Split(toolName, "__")
	// Connected Apps retains its original per-account business naming rather
	// than a stdio MCP server/tool name. Discovery still uses only frozen role
	// specs, and an account namespace must never capture another binding.
	if len(parts) == 5 && parts[0] == "mcp" && parts[1] == "apps" && parts[2] == "b" && parts[3] != "" && (parts[4] == "search_tools" || parts[4] == "execute") {
		name := "apps__b__" + parts[3]
		return namespace == name || namespace == "mcp__"+name
	}
	return len(parts) == 3 && parts[0] == "mcp" && parts[1] != "" && parts[2] != "" && (namespace == parts[1] || namespace == "mcp__"+parts[1])
}

// Types describe shape; complete validation constraints remain in InputSchema.
func typeScript(value any) string {
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
			types = append(types, typeScript(v))
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
		return "ReadonlyArray<" + typeScript(s["items"]) + ">"
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
			fields = append(fields, string(name)+optional+": "+typeScript(properties[key]))
		}
		if s["additionalProperties"] != false {
			fields = append(fields, "[key: string]: unknown")
		}
		return "{ " + strings.Join(fields, "; ") + " }"
	}
	return "unknown"
}

// Computer bridge helpers return ephemeral lease tokens, never public Tool data.
func ComputerBridge(name string) bool {
	return name == "builtin__computer_discover" || name == "builtin__computer_bind" || name == "builtin__computer_invoke"
}
