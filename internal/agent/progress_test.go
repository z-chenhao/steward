package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestProgressGuidancePreservesSharedContextAndChildInheritance(t *testing.T) {
	original := InitialContext(ContextOptions{Position: "root", Title: "exact title", Task: "PRIVATE_TASK"})
	for _, instructions := range []string{"- Revised stage method.", ""} {
		next, ok := ApplyRootProgressGuidance(original, instructions)
		if !ok {
			t.Fatal("Root contribution rejected")
		}
		before, _ := FrozenSystemSections(original)
		after, _ := FrozenSystemSections(next)
		if !reflect.DeepEqual(before[:3], after[:3]) || before[3].ChildText != after[3].ChildText || !reflect.DeepEqual(original[1:], next[1:]) {
			t.Fatal("progress replaced role, capabilities, safety or Task")
		}
		child := InitialContext(ContextOptions{Position: "child", Task: "EXPLICIT_CHILD_INPUT", ParentSystem: next})
		cs, _ := FrozenSystemSections(child)
		if cs[3].Text != before[3].ChildText || cs[3].Progress {
			t.Fatal("Child inherited Root guidance")
		}
		if after[3].Progress != (instructions != "") {
			t.Fatal("opt-in not owned by Host")
		}
	}
	for _, instructions := range []string{strings.Repeat("x", 4097), string([]byte{0xff})} {
		if _, ok := ApplyRootProgressGuidance(original, instructions); ok {
			t.Fatal("invalid contribution accepted")
		}
	}
	child := InitialContext(ContextOptions{Position: "child", Task: "input"})
	if _, ok := ApplyRootProgressGuidance(child, "new"); ok {
		t.Fatal("Child was opted into Root progress")
	}
}
