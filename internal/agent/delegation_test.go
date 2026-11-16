package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/tool"
)

func TestDelegationMethodsPreserveFrozenRolesInputsAndChildTools(t *testing.T) {
	specs := []tool.Spec{AgentToolDefinition().Spec, {Name: "builtin__read", Permission: "read_only"}}
	original := InitialContext(ContextOptions{Position: "root", Tools: specs, Title: "Original anchor", Task: "PRIVATE_ROOT_INPUT"})
	next, ok := ApplyDelegationMethods(original, specs, "Delegation: VERSIONED_METHOD.")
	if !ok {
		t.Fatal("Root method contribution rejected")
	}
	before, _ := FrozenSystemSections(original)
	after, _ := FrozenSystemSections(next)
	for _, i := range []int{0, 2, 3} {
		if !reflect.DeepEqual(before[i], after[i]) {
			t.Fatal("delegation changed identity, safety or execution", i)
		}
	}
	if !reflect.DeepEqual(original[1:], next[1:]) {
		t.Fatal("method changed original Task or runtime messages")
	}
	childSpecs := ChildTools(specs)
	child := InitialContext(ContextOptions{Position: "child", Tools: childSpecs, Task: "EXPLICIT_CHILD_INPUT", ParentSystem: next})
	sections, _ := FrozenSystemSections(child)
	if sections[1].DelegationText != "Delegation: VERSIONED_METHOD." || !strings.Contains(sections[1].Text, "VERSIONED_METHOD") || sections[3].Progress || strings.Contains(sections[3].Text, "Before tools, when the execution stage") {
		t.Fatal("Child lost method or inherited Root-only progress")
	}
	if strings.Contains(child[len(child)-1].Content[0].Text, "PRIVATE_ROOT_INPUT") {
		t.Fatal("Child inherited original Root work")
	}
	for _, instructions := range []string{"", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if _, ok := ApplyDelegationMethods(original, specs, instructions); ok {
			t.Fatal("invalid contribution accepted")
		}
	}
}
