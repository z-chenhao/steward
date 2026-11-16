package sqlite

import (
	"fmt"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/task"
)

// Only resources whose accepted graph predates schedule-instruction-label use
// the original formatter. New graphs with missing facts are rejected. Retire
// this compatibility function with those historical resources.
func legacyAutomationInstructionLabel(text string) string { return task.Title(text) }

// Only immutable Runs without schedule-threshold/1 retain this interpretation.
// Remove after those Runs drain; managed failures never reach it.
func legacyAutomationThreshold(value, below int64, entered, fresh bool) (bool, bool) {
	if !fresh {
		return entered, false
	}
	inside := value < below
	return inside, inside && !entered
}

// Only admission before the threshold-method activation retains native wording.
// Remove after those installations and their immutable Runs are retired.
func legacyAutomationObservationInstruction(below int64) string {
	return fmt.Sprintf("Record the observed integer value with %s. Only entering below %d warrants a notification; never invent an observation.", automation.ObserveName, below)
}
