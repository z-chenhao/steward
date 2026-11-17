package web

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"go.yaml.in/yaml/v3"
)

// The owner submitted only visible fields. Candidate code cannot use their
// Save click to replace advanced settings or another provider's retained data.
// These are private authorization paths, not a general configuration patch API.
func capabilityEditablePaths(id string) []string {
	switch id {
	case "memory":
		return []string{"encryption_key_ref"}
	case "embedding":
		return []string{"provider", "endpoint", "model", "dimensions", "api_key_ref"}
	case "skills":
		return []string{"root", "scope"}
	case "execution":
		return []string{"root", "toolchain_roots"}
	case "mcp":
		return []string{"name", "command", "directory", "args", "tools"}
	case "computer":
		return []string{"computer.root", "computer.endpoint", "computer.compatibility.provider_version", "computer.compatibility.guest_build", "computer.compatibility.driver_version", "computer.compatibility.driver_sha256", "computer.compatibility.driver_team", "computer.compatibility.driver_bundle", "computer.compatibility.architecture", "computer.guest.user", "computer.guest.port", "computer.guest.password_ref", "computer.guest.host_key_sha256", "computer.manifest.version", "javascript.command", "javascript.script"}
	}
	return nil
}

type protectedCapabilityNode struct {
	Kind       yaml.Kind
	Tag, Value string
	Content    []protectedCapabilityNode
}

func protectedCapability(n *yaml.Node, path string, editable []string) *protectedCapabilityNode {
	for _, allowed := range editable {
		if path == allowed {
			return nil
		}
	}
	out := &protectedCapabilityNode{Kind: n.Kind, Tag: n.Tag, Value: n.Value}
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			next := n.Content[i].Value
			if path != "" {
				next = path + "." + next
			}
			child := protectedCapability(n.Content[i+1], next, editable)
			if child != nil {
				out.Content = append(out.Content, protectedCapabilityNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: n.Content[i].Value}, *child)
			}
		}
		if len(out.Content) == 0 && path != "" {
			for _, allowed := range editable {
				if strings.HasPrefix(allowed, path+".") {
					return nil
				}
			}
		}
	} else {
		for _, child := range n.Content {
			value := protectedCapability(child, path, editable)
			if value != nil {
				out.Content = append(out.Content, *value)
			}
		}
	}
	return out
}
func parseGuardDocument(text string, sequence bool) (*yaml.Node, error) {
	kind, tag := yaml.MappingNode, "!!map"
	if sequence {
		kind, tag = yaml.SequenceNode, "!!seq"
	}
	empty := func() *yaml.Node { return &yaml.Node{Kind: kind, Tag: tag} }
	if strings.TrimSpace(text) == "" {
		return empty(), nil
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(text))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return nil, ErrCapabilityInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrCapabilityInvalid
	}
	node := document.Content[0]
	if node.Tag == "!!null" {
		return empty(), nil
	}
	var controlled func(*yaml.Node) bool
	controlled = func(n *yaml.Node) bool {
		if n.Kind == yaml.AliasNode {
			return false
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
					return false
				}
				seen[key.Value] = true
			}
		}
		for _, child := range n.Content {
			if !controlled(child) {
				return false
			}
		}
		return true
	}
	if node.Kind != kind || !controlled(node) {
		return nil, ErrCapabilityInvalid
	}
	return node, nil
}
func guardCapabilityCandidate(in contract.Request, out contract.Result) error {
	get := func(name string) string {
		for _, field := range in.Values {
			if field.Name == name {
				return field.Value
			}
		}
		return ""
	}
	if out.Text == "" {
		if get("mode") == "off" || (get("mode") == "" && get("enabled") != "yes") || (in.ID == "mcp" && in.Action == "remove-provider-0" && get("provider_count") == "1") {
			return nil
		}
		return ErrCapabilityInvalid
	}
	if get("mode") == "automatic" {
		switch in.ID {
		case "memory", "embedding", "skills", "computer":
			if strings.TrimSpace(out.Text) == "mode: automatic" {
				return nil
			}
		}
		return ErrCapabilityInvalid
	}
	baseline := in.Text
	if strings.TrimSpace(baseline) == "mode: automatic" {
		baseline = ""
	}
	old, err := parseGuardDocument(baseline, in.ID == "mcp")
	if err != nil {
		return ErrCapabilityInvalid
	}
	next, err := parseGuardDocument(out.Text, in.ID == "mcp")
	if err != nil {
		return ErrCapabilityInvalid
	}
	if !capabilityIntentBindings(in, next, out) {
		return ErrCapabilityInvalid
	}
	same := func(a, b *yaml.Node) bool {
		x, _ := json.Marshal(protectedCapability(a, "", capabilityEditablePaths(in.ID)))
		y, _ := json.Marshal(protectedCapability(b, "", capabilityEditablePaths(in.ID)))
		return string(x) == string(y)
	}
	if in.ID != "mcp" {
		if !same(old, next) {
			return ErrCapabilityInvalid
		}
		return nil
	}
	if len(next.Content) != len(out.Form.Providers) {
		return ErrCapabilityInvalid
	}
	seen := map[int]bool{}
	for i, provider := range out.Form.Providers {
		index, err := strconv.Atoi(provider.Original)
		if err != nil || index < -1 || index >= len(old.Content) || (index >= 0 && seen[index]) {
			return ErrCapabilityInvalid
		}
		previous := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if index >= 0 {
			seen[index] = true
			previous = old.Content[index]
		}
		if !same(previous, next.Content[i]) {
			return ErrCapabilityInvalid
		}
	}
	return nil
}
