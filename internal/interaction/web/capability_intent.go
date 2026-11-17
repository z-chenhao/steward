package web

import (
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"go.yaml.in/yaml/v3"
)

// Request fields are the owner's authority. A candidate encoder may change its
// implementation, never substitute credentials, endpoints, roots or tool grants.
func capabilityIntentBindings(in contract.Request, next *yaml.Node, out contract.Result) bool {
	get := func(name string) string {
		for _, field := range in.Values {
			if field.Name == name {
				return field.Value
			}
		}
		return ""
	}
	field := func(node *yaml.Node, path string) *yaml.Node {
		for _, key := range strings.Split(path, ".") {
			if node == nil || node.Kind != yaml.MappingNode {
				return nil
			}
			var found *yaml.Node
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Value == key {
					found = node.Content[i+1]
					break
				}
			}
			node = found
		}
		return node
	}
	scalar := func(node *yaml.Node, path, value string) bool {
		n := field(node, path)
		if n == nil {
			return value == ""
		}
		return n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value == value
	}
	integer := func(node *yaml.Node, path, value string) bool {
		n := field(node, path)
		if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
			return false
		}
		actual, e := strconv.Atoi(n.Value)
		expected, err := strconv.Atoi(value)
		return e == nil && err == nil && actual == expected
	}
	lines := func(node *yaml.Node, path, value string) bool {
		n := field(node, path)
		if n == nil || n.Kind != yaml.SequenceNode {
			return false
		}
		expected := []string{}
		for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
			if line != "" {
				expected = append(expected, line)
			}
		}
		if len(expected) != len(n.Content) {
			return false
		}
		for i, v := range n.Content {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value != expected[i] {
				return false
			}
		}
		return true
	}
	switch in.ID {
	case "memory":
		return scalar(next, "encryption_key_ref", get("key_ref"))
	case "embedding":
		return scalar(next, "endpoint", get("endpoint")) && scalar(next, "model", get("model")) && scalar(next, "api_key_ref", get("key_ref")) && integer(next, "dimensions", get("dimensions"))
	case "skills":
		return scalar(next, "root", get("root")) && scalar(next, "scope", get("scope"))
	case "execution":
		return scalar(next, "root", get("root")) && lines(next, "toolchain_roots", get("toolchains"))
	case "computer":
		for _, binding := range []struct{ path, name string }{{"computer.root", "root"}, {"computer.endpoint", "endpoint"}, {"computer.compatibility.provider_version", "provider_version"}, {"computer.compatibility.guest_build", "guest_build"}, {"computer.compatibility.driver_version", "driver_version"}, {"computer.compatibility.driver_sha256", "driver_sha256"}, {"computer.compatibility.driver_team", "driver_team"}, {"computer.compatibility.driver_bundle", "driver_bundle"}, {"computer.compatibility.architecture", "architecture"}, {"computer.guest.user", "guest_user"}, {"computer.guest.password_ref", "password_ref"}, {"computer.guest.host_key_sha256", "host_key"}, {"javascript.command", "node"}, {"javascript.script", "script"}} {
			if !scalar(next, binding.path, get(binding.name)) {
				return false
			}
		}
		return integer(next, "computer.guest.port", get("guest_port")) && integer(next, "computer.manifest.version", get("manifest_version"))
	case "mcp":
		count, err := strconv.Atoi(get("provider_count"))
		if err != nil || count < 1 || count > 32 {
			return false
		}
		indices := []int{}
		for i := 0; i < count; i++ {
			if in.Action != "remove-provider-"+strconv.Itoa(i) {
				indices = append(indices, i)
			}
		}
		if len(indices) != len(next.Content) || len(indices) != len(out.Form.Providers) {
			return false
		}
		for i, index := range indices {
			prefix := "provider." + strconv.Itoa(index) + "."
			node := next.Content[i]
			if out.Form.Providers[i].Original != get(prefix+"original") || !scalar(node, "name", get(prefix+"name")) || !scalar(node, "command", get(prefix+"command")) || !scalar(node, "directory", get(prefix+"directory")) || !lines(node, "args", get(prefix+"args")) {
				return false
			}
			tools := field(node, "tools")
			toolCount, err := strconv.Atoi(get(prefix + "tool_count"))
			if err != nil || toolCount < 1 || toolCount > 128 || tools == nil || tools.Kind != yaml.MappingNode {
				return false
			}
			names := map[string]bool{}
			expectedCount := 0
			for j := 0; j < toolCount; j++ {
				if in.Action == "remove-tool-"+strconv.Itoa(index)+"-"+strconv.Itoa(j) {
					continue
				}
				p := prefix + "tool." + strconv.Itoa(j) + "."
				name, permission := get(p+"name"), get(p+"permission")
				if name == "" || names[name] || !literalCapabilityGrant(tools, name, permission) {
					return false
				}
				names[name] = true
				expectedCount++
			}
			if len(tools.Content) != 2*expectedCount {
				return false
			}
		}
		return true
	}
	return false
}

func literalCapabilityGrant(node *yaml.Node, name, value string) bool {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			n := node.Content[i+1]
			return n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value == value
		}
	}
	return false
}
