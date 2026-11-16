package codemode

import (
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/tool"
)

func TestConnectedAppNamespacesKeepExactAccountBoundary(t *testing.T) {
	for _, test := range []struct {
		tool, namespace string
		matches         bool
	}{
		{"mcp__apps__b__personal__search_tools", "apps__b__personal", true},
		{"mcp__apps__b__personal__execute", "mcp__apps__b__personal", true},
		{"mcp__apps__b__personal__execute", "apps__b__work", false},
		{"mcp__apps__b__personal__execute", "apps", false},
		{"mcp__apps__b____execute", "apps__b__", false},
		{"mcp__apps__b__personal__execute__other", "apps__b__personal", false},
		{"mcp__apps__read", "apps", true},
		{"mcp__fixture__read", "fixture", true},
	} {
		if got := NamespaceMatches(test.tool, test.namespace); got != test.matches {
			t.Errorf("namespace %q matched %q: %v, want %v", test.namespace, test.tool, got, test.matches)
		}
	}
}

func TestDescriptionIncludesSchemaDeclarationsAndInstructions(t *testing.T) {
	spec := tool.Spec{Name: "builtin__agent_run", Description: "Create one Child, never a Task.", InputSchema: `{"type":"object","properties":{"task":{"type":"string"},"artifactRefs":{"type":"array","items":{"type":"string"}}},"required":["task"],"additionalProperties":false}`}
	spec.ModelInputSchema = `{"type":"object","description":"Compact direct declaration"}`
	d := Describe(spec)
	for _, fragment := range []string{`"task": string`, `"artifactRefs"?: ReadonlyArray<string>`, `readonly artifactRefs: readonly string[]`} {
		if !strings.Contains(d.Declaration, fragment) {
			t.Fatal(d.Declaration)
		}
	}
	if d.InputSchema != spec.InputSchema || d.Instructions != spec.Description {
		t.Fatal(d)
	}
	if !NamespaceMatches("mcp__fixture__echo", "fixture") || !NamespaceMatches("mcp__mail__read", "mcp__mail") || NamespaceMatches("mcp__mail__read", "agent") {
		t.Fatal("namespace mapping")
	}
}

func TestDefinitionExplainsUnifiedCapabilities(t *testing.T) {
	description := Definition().Spec.Description
	for _, required := range []string{"tools[name](args)", "builtin__skill_list", "builtin__skill_view", `describeNamespace("cua")`, "cua.getState()", "documentation()", "observe after actions", `describeNamespace("agent")`, "agent.run({task,artifactRefs})", "Main has no agent namespace", "{text,artifactRefs}", "unawaited", "Approval"} {
		if !strings.Contains(description, required) {
			t.Fatalf("missing %q in %s", required, description)
		}
	}
}

func TestNamespacePreservesSingleUnderscoresAndSourceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, namespace string
		want            bool
	}{
		{"builtin__task_search", "task", true},
		{"builtin__task_search", "builtin__task", true},
		{"builtin__task_search", "tasks", false},
		{"builtin__task__search", "task", false},
		{"mcp__browser_use__click_button", "browser_use", true},
		{"mcp__browser_use__click_button", "mcp__browser_use", true},
		{"mcp__browser_use__click_button", "browser", false},
		{"mcp__browser_use__click_button", "builtin__browser_use", false},
		{"mcp__browser_use__click_button", "click_button", false},
		{"mcp____click_button", "", false},
	} {
		if got := NamespaceMatches(tc.name, tc.namespace); got != tc.want {
			t.Errorf("NamespaceMatches(%q, %q)=%v, want %v", tc.name, tc.namespace, got, tc.want)
		}
	}
}
