package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Resolve physical isolation from Host facts, never a model-supplied path.
// One Task's Root and Children retain one conflict key; a shared workspace or
// desktop keeps the original keys. Accepted invocations are never rewritten.
func (t *tools) environmentConflictKeys(ctx context.Context, tx *sql.Tx, spec tool.Spec, original []string) ([]string, error) {
	if !environment.IsTool(spec.Name) || spec.ConflictPolicy != "execution_workspace" || t.runs.computerProvider != nil || t.runs.localEnvironment == nil {
		return original, nil
	}
	var taskID, scope, directory, status sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT r.task_id,e.scope_hash,e.directory,e.status FROM run r LEFT JOIN task_code_environment e USING(task_id) WHERE r.run_id=?`, t.claim.RunID()).Scan(&taskID, &scope, &directory, &status)
	if err != nil {
		return nil, err
	}
	if !scope.Valid {
		return original, nil
	}
	if !taskID.Valid || status.String != "ready" || scope.String != environment.ResourceHash(*t.runs.localEnvironment) || scope.String != spec.ResourceHash || directory.String != coding.Location(t.runs.host.codeRoot, taskID.String).Directory {
		return nil, llm.ModelIncompatible
	}
	keys := []string{"environment:task:" + taskID.String}
	// Old queued calls may still carry the broad keys. Carry those keys until
	// that Task's old calls drain, so an upgrade cannot split one workspace's
	// serialization boundary. Independent Tasks need no common lock.
	raw, _ := encoded(original)
	var retained bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tool_invocation i JOIN run r USING(run_id) WHERE r.task_id=? AND i.status IN ('queued','waiting','running') AND EXISTS(SELECT 1 FROM json_each(i.conflict_keys) a JOIN json_each(?) b ON a.value=b.value))`, taskID.String, raw).Scan(&retained)
	if retained {
		keys = append(keys, original...)
	}
	return keys, err
}
