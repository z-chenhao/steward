package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNamespaceSourceIsPrivateCopiedAndSeparateFromFrozenDescription(t *testing.T) {
	definition, _ := FixtureRegistry().Resolve("mcp__fixture__echo")
	source := "PRIVATE_ORIGINAL_DISCOVERY_SOURCE\nnot a model declaration"
	definition.NamespaceSource = &source
	definition.Spec.NamespaceDescription = "Frozen public summary"
	registry, err := NewRegistry([]Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	source = "caller changed original input"
	resolved, _ := registry.Resolve(definition.Spec.Name)
	if resolved.NamespaceSource == nil || !strings.Contains(*resolved.NamespaceSource, "PRIVATE_ORIGINAL_DISCOVERY_SOURCE") {
		t.Fatal("caller rewrote original discovery metadata")
	}
	*resolved.NamespaceSource = "caller changed resolved metadata"
	second, _ := registry.Resolve(definition.Spec.Name)
	if !strings.Contains(*second.NamespaceSource, "PRIVATE_ORIGINAL_DISCOVERY_SOURCE") {
		t.Fatal("resolved metadata aliased the live registry")
	}
	raw, err := json.Marshal(registry.Describe())
	if err != nil || strings.Contains(string(raw), "PRIVATE_ORIGINAL_DISCOVERY_SOURCE") || !strings.Contains(string(raw), "Frozen public summary") {
		t.Fatal("private source entered serialized capability", string(raw), err)
	}
	selected, err := registry.Select(registry.Describe())
	if err != nil || selected.Describe()[0].NamespaceDescription != "Frozen public summary" {
		t.Fatal("private discovery source changed the original frozen view", err)
	}
}
