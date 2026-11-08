package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (r *Runs) bindTaskCode(ctx context.Context, tx *sql.Tx, id string) error {
	var scope string
	err := tx.QueryRowContext(ctx, `SELECT scope_hash FROM task_code_environment WHERE task_id=?`, id).Scan(&scope)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if r.localEnvironment == nil || scope != environment.ResourceHash(*r.localEnvironment) {
			return llm.ModelIncompatible
		}
		return nil
	}
	if r.localEnvironment == nil || r.localEnvironment.RepositorySource == "" {
		return nil
	}
	cfg := *r.localEnvironment
	location := coding.Location(r.host.codeRoot, id)
	_, err = tx.ExecContext(ctx, `INSERT INTO task_code_environment(task_id,scope_hash,source_path,directory,baseline_path,status) VALUES(?,?,?,?,?,'pending')`, id, environment.ResourceHash(cfg), cfg.RepositorySource, location.Directory, location.Baseline)
	return err
}

func (r *Runs) prepareTaskCode(ctx context.Context, c agent.RunClaim, run agent.Run) error {
	var id, scope, source, status string
	var existing coding.Workspace
	err := r.host.db.QueryRowContext(ctx, `SELECT e.task_id,e.scope_hash,e.source_path,e.status,e.directory,e.baseline_path,e.baseline_hash FROM task_code_environment e JOIN run r USING(task_id) WHERE r.run_id=?`, c.RunID()).Scan(&id, &scope, &source, &status, &existing.Directory, &existing.Baseline, &existing.BaselineHash)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return agent.ErrStorage
	}
	if r.localEnvironment == nil || scope != environment.ResourceHash(*r.localEnvironment) {
		return r.failCodeRun(ctx, c, run.Version, llm.ModelIncompatible)
	}
	// Do not bind a prior frozen Run to a newly configured source. Registry
	// selection would reject it too. Fail the accepted Worker in isolation.
	for _, spec := range run.Tools {
		if environment.IsTool(spec.Name) && spec.ResourceHash != scope {
			return r.failCodeRun(ctx, c, run.Version, llm.ModelIncompatible)
		}
	}
	location := coding.Location(r.host.codeRoot, id)
	if existing.Directory != location.Directory || existing.Baseline != location.Baseline {
		return r.failCodeRun(ctx, c, run.Version, llm.ModelIncompatible)
	}
	live, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var fenceErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-live.Done():
				return
			case <-tool.ShutdownSignal(ctx):
				cancel()
				return
			case <-ticker.C:
				if err := r.write(live, func(tx *sql.Tx) error { return fence(live, tx, c) }); err != nil {
					if live.Err() == nil {
						fenceErr = err
					}
					cancel()
					return
				}
			}
		}
	}()
	w := existing
	var prepareErr error
	if status == "ready" {
		prepareErr = coding.Validate(live, existing)
	} else {
		w, prepareErr = coding.Prepare(live, r.host.codeRoot, source, id, scope, func(point string) {
			r.host.boundary(point)
			select {
			case <-tool.ShutdownSignal(ctx):
				cancel()
			default:
			}
		})
	}
	interrupted := live.Err()
	cancel()
	<-done
	select {
	case <-tool.ShutdownSignal(ctx):
		return context.Canceled
	default:
	}
	if fenceErr != nil {
		return fenceErr
	}
	if interrupted != nil || errors.Is(prepareErr, context.Canceled) || errors.Is(prepareErr, context.DeadlineExceeded) {
		return context.Canceled
	}
	if status == "ready" {
		if prepareErr == nil {
			return nil
		}
		return r.failCodeRun(ctx, c, run.Version, llm.ProviderUnavailable)
	}
	state, code := "ready", ""
	if prepareErr != nil {
		state, code = "failed", "CODE_PREPARATION_FAILED"
	}
	err = r.write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		if err := fence(context.WithoutCancel(ctx), tx, c); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.WithoutCancel(ctx), `UPDATE task_code_environment SET status=?,baseline_hash=?,error_code=? WHERE task_id=? AND status!='ready'`, state, w.BaselineHash, code, id)
		return err
	})
	if err != nil {
		return err
	}
	if prepareErr != nil {
		return r.failCodeRun(context.WithoutCancel(ctx), c, run.Version, llm.ProviderUnavailable)
	}
	return nil
}

func (r *Runs) failCodeRun(ctx context.Context, c agent.RunClaim, version int64, reason llm.ProviderError) error {
	if err := r.Fail(ctx, c, version, string(reason)); err != nil {
		return err
	}
	return agent.CommittedRunFailure(reason)
}

const codeReportTask = ` FROM task_code_environment e JOIN task t USING(task_id)
 WHERE t.task_ref=? AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0)
 AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`

func (s *Store) CodeReport(ctx context.Context, ref string, withDiff bool) (coding.Report, error) {
	var out coding.Report
	var taskID string
	err := s.db.QueryRowContext(ctx, `SELECT t.task_id,t.task_ref,e.directory,e.baseline_path,e.baseline_hash,e.status,e.error_code`+codeReportTask, ref).Scan(&taskID, &out.TaskRef, &out.Directory, &out.Baseline, &out.BaselineHash, &out.Status, &out.ErrorCode)
	if err == sql.ErrNoRows {
		return coding.Report{}, agent.ErrNotFound
	}
	if err != nil {
		return coding.Report{}, agent.ErrStorage
	}
	location := coding.Location(s.codeRoot, taskID)
	if out.Directory != location.Directory || out.Baseline != location.Baseline {
		return coding.Report{}, coding.ErrInvalid
	}
	out.Commands = []coding.Command{}
	rows, err := s.db.QueryContext(ctx, `SELECT i.arguments_json,i.status,i.attempt_count,i.created_at,coalesce(i.result_json,'null'),coalesce(i.error_json,'null') FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id
 WHERE r.task_id=? AND EXISTS(SELECT 1 FROM json_each(c.tools_json) j WHERE json_extract(j.value,'$.name')=i.tool_name AND json_extract(j.value,'$.resource_hash')=e.scope_hash) AND i.tool_name='builtin__bash' ORDER BY i.created_at,i.invocation_id LIMIT 257`, taskID)
	if err != nil {
		return coding.Report{}, agent.ErrStorage
	}
	total := 0
	for rows.Next() {
		var args, raw, failure string
		var command coding.Command
		if err = rows.Scan(&args, &command.Status, &command.Attempts, &command.AcceptedAt, &raw, &failure); err != nil {
			rows.Close()
			return coding.Report{}, agent.ErrStorage
		}
		total += len(args) + len(raw) + len(failure)
		if len(out.Commands) == 256 || total > 4<<20 {
			out.CommandsTruncated = true
			break
		}
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &input) != nil {
			rows.Close()
			return coding.Report{}, agent.ErrStorage
		}
		command.Command = input.Command
		command.Result = json.RawMessage(raw)
		command.Error = json.RawMessage(failure)
		out.Commands = append(out.Commands, command)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return coding.Report{}, agent.ErrStorage
	}
	if withDiff {
		if out.Status != "ready" {
			return coding.Report{}, coding.ErrInvalid
		}
		diff, err := coding.Review(ctx, out.Workspace)
		if err != nil {
			return coding.Report{}, err
		}
		out.Diff = &diff
	}
	s.boundary("before_code_report_return")
	var currentID string
	err = s.db.QueryRowContext(ctx, `SELECT t.task_id`+codeReportTask, ref).Scan(&currentID)
	if err == sql.ErrNoRows || err == nil && currentID != taskID {
		return coding.Report{}, agent.ErrNotFound
	}
	if err != nil {
		return coding.Report{}, agent.ErrStorage
	}
	return out, nil
}

// SaveCodeReport stages the bounded snapshot outside SQL and rechecks Task and
// privacy scope in the existing Artifact commit transaction.
func (s *Store) SaveCodeReport(ctx context.Context, ref string) (artifact.Descriptor, error) {
	return s.saveCodeReport(ctx, ref, nil)
}

func (s *Store) saveCodeReport(ctx context.Context, ref string, handoff *codePRHandoff) (artifact.Descriptor, error) {
	report, err := s.CodeReport(ctx, ref, true)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	report.Directory = ""
	if handoff != nil {
		report.PR = &handoff.binding.observation
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if len(raw) > 16<<20 {
		return artifact.Descriptor{}, artifact.ErrLimit
	}
	artifacts := s.Artifacts()
	staged, err := artifacts.Stage(ctx, artifact.Input{Name: "code-review.json", MediaType: "application/json"}, bytes.NewReader(raw))
	if err != nil {
		return artifact.Descriptor{}, err
	}
	defer staged.Close()
	s.boundary("before_code_report_commit")
	var out artifact.Descriptor
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		scope := artifact.Scope{UserID: s.OwnerID(), Owner: true}
		err := tx.QueryRowContext(ctx, `SELECT t.task_id,t.agent_id`+codeReportTask, ref).Scan(&scope.TaskID, &scope.AgentID)
		if err == sql.ErrNoRows {
			return agent.ErrNotFound
		}
		if err != nil {
			return agent.ErrStorage
		}
		if handoff != nil {
			current, err := readCodePRBinding(ctx, tx.QueryRowContext, ref, handoff.cfg, handoff.expected)
			if err != nil {
				return err
			}
			if current != handoff.binding || current.taskID != scope.TaskID {
				return agent.ErrConflict
			}
		}
		artifactRef, err := artifacts.CommitTx(ctx, tx, scope, staged, "")
		if err != nil {
			return err
		}
		out, err = artifacts.DescribeTx(ctx, tx, scope, artifactRef, true)
		return err
	})
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return out, nil
}
