package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

// These methods are retained for pre-plugin Run snapshots and explicit
// never-managed embeddings. Current task-control/task-feedback dispatch must
// fail closed when unavailable; these are not fallback implementations.
// Remove with their historical Run/feedback consumers, preserving receipts.
const legacyFeedbackGuidanceV1 = "If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference;"

func (r *Runs) legacySearchTasksV1(ctx context.Context, tx *sql.Tx, runID string, request task.SearchRequest) (tool.Result, error) {
	candidates, err := r.taskCandidates(ctx, tx, runID, request, 0)
	if err != nil {
		return tool.Result{}, err
	}
	raw, err := encoded(task.Rank(candidates, request.Query, request.Limit))
	return tool.Result{Text: raw}, err
}
