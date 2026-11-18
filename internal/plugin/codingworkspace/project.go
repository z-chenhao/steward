package codingworkspace

import (
	"context"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Project policy owns the small repository document and check selection. This
// is an executable project recipe, not permission to run its commands.
func projectPlan(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.ProjectFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	invalid := func() (rsiplugin.Result, error) {
		return rsiplugin.JSONResult(ctx, contract.ProjectProposal{ErrorCode: "INVALID_ARGUMENT"})
	}
	var document contract.ProjectDocument
	if len(facts.Document) > 32768 || rsiplugin.Decode([]byte(facts.Document), &document) != nil || document.Version != 1 || strings.TrimSpace(document.Name) == "" || len(document.Name) > 128 || len(document.Checks) < 1 || len(document.Checks) > 64 {
		return invalid()
	}
	stepValid := func(s contract.ProjectStep) bool {
		return strings.TrimSpace(s.Command) != "" && len(s.Command) <= 16384 && s.TimeoutSeconds >= 1 && s.TimeoutSeconds <= 120
	}
	if !stepValid(document.Prepare) || !stepValid(document.Doctor) || !stepValid(document.Cleanup) || document.Start != nil && !stepValid(*document.Start) {
		return invalid()
	}
	seen := map[string]bool{}
	var selected *contract.ProjectCheck
	for i, check := range document.Checks {
		if strings.TrimSpace(check.Name) == "" || len(check.Name) > 64 || seen[check.Name] || strings.TrimSpace(check.Requirement) == "" || len(check.Requirement) > 2048 || !stepValid(check.ProjectStep) {
			return invalid()
		}
		if !contract.ValidEvidencePaths(check.Evidence) {
			return invalid()
		}
		seen[check.Name] = true
		if check.Name == facts.Check {
			selected = &document.Checks[i]
		}
	}
	if selected == nil {
		return rsiplugin.JSONResult(ctx, contract.ProjectProposal{ErrorCode: "NOT_FOUND"})
	}
	total := document.Prepare.TimeoutSeconds + document.Doctor.TimeoutSeconds + selected.TimeoutSeconds + document.Cleanup.TimeoutSeconds
	if total > 240 {
		return invalid()
	}
	return rsiplugin.JSONResult(ctx, contract.ProjectProposal{Plan: &contract.ProjectPlan{Evidence: selected.Evidence, Version: 1, Name: document.Name, Check: selected.Name, Requirement: selected.Requirement, Prepare: document.Prepare, Start: document.Start, Doctor: document.Doctor, Verify: selected.ProjectStep, Cleanup: document.Cleanup}})
}
