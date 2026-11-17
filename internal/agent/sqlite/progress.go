package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
)

// Progress records meaningful Host/Worker progress under its current claim. It
// creates neither TaskUpdate, Inbox, Outbox wake nor user delivery. Tool traces
// are not synthesized into progress. A later UI read observes this projection.
func (r *Runs) Progress(ctx context.Context, c agent.RunClaim, summary string) error {
	if strings.TrimSpace(summary) == "" || len(summary) > 512 || !utf8.ValidString(summary) {
		return agent.ErrConflict
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		return saveProgress(ctx, tx, c, summary)
	})
}

// Only opted-in Root assistant prose accompanying tools is a stage update.
// Never publish Child text, final results, compaction, deltas or Tool payloads.
// The checkpoint and summary commit together, so recovery reuses the generation
// without regenerating text or exposing progress ahead of a durable boundary.
func progressText(cp agent.Checkpoint) string {
	if cp.Phase != agent.ToolsPhase || cp.Result == nil || cp.Result.Clipped || len(cp.Result.ToolCalls) == 0 {
		return ""
	}
	text := strings.Join(strings.Fields(cp.Result.Text), " ")
	if text == "" || len(text) > 512 || !utf8.ValidString(text) || strings.ContainsFunc(text, unicode.IsControl) {
		return ""
	}
	return text
}

func progressEligible(ctx context.Context, tx *sql.Tx, runID string) (bool, error) {
	var eligible bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run r,json_each(r.checkpoint_json,'$.messages') m,json_each(m.value,'$.system_sections') section WHERE r.run_id=? AND r.kind='worker' AND r.worker_position='root' AND json_extract(m.value,'$.type')='system' AND (json_extract(section.value,'$.kind')='progress_guidance' OR (json_extract(section.value,'$.kind')='execution' AND json_extract(section.value,'$.progress')=1)))`, runID).Scan(&eligible)
	return eligible, err
}

func (r *Runs) checkpointProgress(ctx context.Context, tx *sql.Tx, c agent.RunClaim, cp agent.Checkpoint, p progressSelection) (bool, error) {
	text := progressText(cp)
	if text == "" || (p.managed && (!p.publish || p.summary != text)) {
		return false, nil
	}
	eligible, err := progressEligible(ctx, tx, c.RunID())
	if err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	if p.managed {
		if err := r.pluginDispatchFence(ctx, tx, c.RunID()); err != nil {
			if progressUnavailable(err) {
				return false, nil
			}
			return false, err
		}
	}
	if err := saveProgress(ctx, tx, c, text); err != nil {
		return false, err
	}
	r.host.boundary("before_progress_commit")
	return true, nil
}

// The caller owns the fenced transaction. Identical summaries do not churn the
// Task version or timestamp; no event, delivery or Main input is created here.
func saveProgress(ctx context.Context, tx *sql.Tx, c agent.RunClaim, summary string) error {
	var id, old string
	if err := tx.QueryRowContext(ctx, `SELECT t.task_id,t.progress_summary FROM task t JOIN run r USING(task_id) WHERE r.run_id=? AND r.kind='worker' AND r.worker_position='root'`, c.RunID()).Scan(&id, &old); err != nil {
		return agent.ErrConflict
	}
	if old == summary {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE task SET progress_summary=?,updated_at=?,version=version+1 WHERE task_id=?`, summary, time.Now().UnixMicro(), id)
	return err
}
