package provider

import "testing"

func TestToolOutputTimingPrecedesCompleteArguments(t *testing.T) {
	for _, name := range []string{"metadata", "arguments"} {
		t.Run(name, func(t *testing.T) {
			s := newStreamState(Model{}, nil)
			var marks []string
			s.onOutput = func(phase string) { marks = append(marks, phase) }
			s.tool(0, "", "", "")
			s.args(0, "")
			if len(marks) != 0 {
				t.Fatal("empty protocol frames counted as output", marks)
			}
			if name == "metadata" {
				s.tool(0, "call-1", "task_manage", "")
			} else {
				s.args(0, `{"operation":`)
			}
			if len(marks) != 1 || marks[0] != "first_tool_call" || s.tools[0].ended {
				t.Fatal("tool output timing waited for completed arguments", marks)
			}
		})
	}
}
