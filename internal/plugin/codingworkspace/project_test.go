package codingworkspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestCodingProjectPolicySelectsNamedRequirementWithoutRewritingCommands(t *testing.T) {
	step := contract.ProjectStep{Command: "printf original", TimeoutSeconds: 5}
	doc := contract.ProjectDocument{Version: 1, Name: "project", Prepare: step, Doctor: step, Cleanup: step, Checks: []contract.ProjectCheck{{Name: "first", Requirement: "First user-visible behavior", ProjectStep: step}, {Evidence: []string{".evidence/flow.png"}, Name: "second", Requirement: "Second user-visible behavior", ProjectStep: contract.ProjectStep{Command: "printf selected", TimeoutSeconds: 7}}}}
	raw, _ := json.Marshal(doc)
	input, _ := json.Marshal(contract.ProjectFacts{Document: string(raw), Check: "second"})
	result, err := Plugin().Services[contract.ProjectContract](context.Background(), rsiplugin.Call{Input: input})
	if err != nil {
		t.Fatal(err)
	}
	var selected contract.ProjectProposal
	if rsiplugin.DecodeResult(result.Output, &selected) != nil || selected.Plan == nil || selected.ErrorCode != "" || selected.Plan.Verify.Command != "printf selected" || selected.Plan.Verify.TimeoutSeconds != 7 || selected.Plan.Requirement != "Second user-visible behavior" || selected.Plan.Prepare.Command != step.Command || len(selected.Plan.Evidence) != 1 || selected.Plan.Evidence[0] != ".evidence/flow.png" {
		t.Fatal("selection rewrote the declared contract", string(result.Output))
	}
}

func TestCodingProjectPolicyRejectsAmbiguousOrUnboundedRecipes(t *testing.T) {
	step := contract.ProjectStep{Command: "true", TimeoutSeconds: 5}
	doc := contract.ProjectDocument{Version: 1, Name: "project", Prepare: step, Doctor: step, Cleanup: step, Checks: []contract.ProjectCheck{{Name: "build", Requirement: "Build compiles", ProjectStep: step}}}
	for _, c := range []struct {
		name   string
		mutate func(*contract.ProjectDocument)
		check  string
		want   string
	}{
		{"unknown-check", func(*contract.ProjectDocument) {}, "missing", "NOT_FOUND"},
		{"duplicate-check", func(d *contract.ProjectDocument) { d.Checks = append(d.Checks, d.Checks[0]) }, "build", "INVALID_ARGUMENT"},
		{"missing-requirement", func(d *contract.ProjectDocument) { d.Checks[0].Requirement = "" }, "build", "INVALID_ARGUMENT"},
		{"missing-doctor", func(d *contract.ProjectDocument) { d.Doctor = contract.ProjectStep{} }, "build", "INVALID_ARGUMENT"},
		{"oversized-step", func(d *contract.ProjectDocument) { d.Prepare.TimeoutSeconds = 121 }, "build", "INVALID_ARGUMENT"},
		{"oversized-total", func(d *contract.ProjectDocument) { d.Prepare.TimeoutSeconds = 120; d.Checks[0].TimeoutSeconds = 120 }, "build", "INVALID_ARGUMENT"},
		{"evidence-traversal", func(d *contract.ProjectDocument) { d.Checks[0].Evidence = []string{"../private"} }, "build", "INVALID_ARGUMENT"},
		{"evidence-duplicate", func(d *contract.ProjectDocument) { d.Checks[0].Evidence = []string{"out", "out"} }, "build", "INVALID_ARGUMENT"},
		{"evidence-absolute", func(d *contract.ProjectDocument) { d.Checks[0].Evidence = []string{"/etc/passwd"} }, "build", "INVALID_ARGUMENT"},
	} {
		t.Run(c.name, func(t *testing.T) {
			copy := doc
			copy.Checks = append([]contract.ProjectCheck{}, doc.Checks...)
			c.mutate(&copy)
			raw, _ := json.Marshal(copy)
			input, _ := json.Marshal(contract.ProjectFacts{Document: string(raw), Check: c.check})
			result, err := projectPlan(context.Background(), rsiplugin.Call{Input: input})
			if err != nil {
				t.Fatal(err)
			}
			var proposal contract.ProjectProposal
			if rsiplugin.DecodeResult(result.Output, &proposal) != nil || proposal.Plan != nil || proposal.ErrorCode != c.want {
				t.Fatal(string(result.Output))
			}
		})
	}
}
