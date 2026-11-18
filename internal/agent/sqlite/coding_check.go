package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// A check has no authority over the author workspace after its bounded copy.
// Its original ToolInvocation owns approval, terminal receipt and non-replay.
func (t *tools) executeCodingCheck(ctx context.Context, inv tool.Invocation) (result tool.Result, executionErr error) {
	var request coding.CheckRequest
	var workspace coding.Workspace
	var name, args, taskID, scope, resource string
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		role, savedName, raw, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if role != "worker" || !coding.IsCheckTool(savedName) {
			return tool.ErrApprovalDenied
		}
		name, args = savedName, raw
		if err := json.Unmarshal([]byte(args), &request); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT r.task_id,e.scope_hash,e.directory,e.status FROM run r JOIN task_code_environment e USING(task_id) WHERE r.run_id=?`, t.claim.RunID()).Scan(&taskID, &scope, &workspace.Directory, &workspace.Status); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT json_extract(j.value,'$.resource_hash') FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE r.run_id=? AND json_extract(j.value,'$.name')=?`, t.claim.RunID(), name).Scan(&resource)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	if t.runs.computerProvider != nil || t.runs.localEnvironment == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	cfg := *t.runs.localEnvironment
	expectedResource := scope
	if name == coding.VerifyTool {
		if cfg.ProjectContainer == nil {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		expectedResource = cfg.ProjectContainer.ResourceHash(scope)
	}
	if scope != environment.ResourceHash(cfg) || resource != expectedResource || workspace.Status != "ready" || workspace.Directory != coding.Location(t.runs.host.codeRoot, taskID).Directory {
		return tool.Failure("PERMISSION_DENIED"), nil
	}

	// Do not let a lost Run/Code claim, Forget or Guardian stop leave a verifier
	// running. Preserve the authority error rather than accepting partial output.
	live, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var authorityErr error
	go func() {
		defer close(done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-live.Done():
				return
			case <-tick.C:
				err := t.runs.write(live, func(tx *sql.Tx) error {
					if err := t.fence(live, tx); err != nil {
						return err
					}
					return t.runs.pluginDispatchFence(live, tx, t.claim.RunID())
				})
				if err != nil {
					if live.Err() == nil {
						authorityErr = err
					}
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-done
		if authorityErr != nil {
			result = tool.Result{}
			executionErr = authorityErr
		}
	}()
	candidate, err := coding.CaptureCandidate(live, workspace)
	if err != nil {
		if live.Err() != nil {
			return tool.Failure("CANCELLED"), nil
		}
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	defer candidate.Close()
	if name == coding.StatusTool {
		checks, truncated, err := t.runs.host.readCodingChecks(live, taskID, candidate.SHA256, 64, 256<<10)
		if err != nil {
			return tool.Result{}, err
		}
		if err := t.runs.evidenceReadTx(live, func(tx *sql.Tx) error { return t.fence(live, tx) }); err != nil {
			return tool.Result{}, err
		}
		status := coding.CheckStatus{CandidateSHA256: candidate.SHA256, Checks: checks, ChecksTruncated: truncated}
		if err := t.projectEnvironmentStatus(live, taskID, cfg, &status); err != nil {
			return tool.Result{}, err
		}
		if request.ProjectFile != "" || request.Check != "" {
			if request.ProjectFile == "" || request.Check == "" {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			selection, _, err := t.projectSelection(live, candidate.Directory, coding.Location(t.runs.host.codeRoot, taskID).Baseline, request.ProjectFile, request.Check)
			if err != nil {
				return environmentMethodFailure(err)
			}
			status.Project = &selection
		}
		if err := t.runs.evidenceReadTx(live, func(tx *sql.Tx) error {
			if err := t.fence(live, tx); err != nil {
				return err
			}
			return t.runs.pluginDispatchFence(live, tx, t.claim.RunID())
		}); err != nil {
			return tool.Result{}, err
		}
		raw, err := json.Marshal(status)
		return tool.Result{Text: string(raw)}, err
	}
	if name == coding.VerifyTool {
		return t.executeProjectVerification(live, inv, candidate, cfg, taskID, scope, args)
	}
	if candidate.SHA256 != request.CandidateSHA256 {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	if t.runs.plugins == nil {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(live, t.claim.RunID())
	if err != nil {
		return environmentMethodFailure(err)
	}
	if revision == "" {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	if err := h.Store.CheckGraph(live, revision); err != nil {
		return environmentMethodFailure(err)
	}
	input, _ := json.Marshal(contract.CheckFacts{Ceilings: contract.CheckLimits{TimeoutSeconds: 120, OutputBytes: 48000}, RequestedTimeout: request.Timeout})
	raw, err := h.ReadService(live, revision, contract.CheckContract, input, nil)
	if err != nil {
		return environmentMethodFailure(err)
	}
	var limits contract.CheckLimits
	if rsiplugin.DecodeResult(raw, &limits) != nil || limits.TimeoutSeconds < 1 || limits.TimeoutSeconds > 120 || limits.OutputBytes < 1 || limits.OutputBytes > 48000 || request.Timeout != nil && limits.TimeoutSeconds > *request.Timeout {
		return environmentMethodFailure(rsi.ErrInvalid)
	}
	driver, err := environment.ShellDriverFingerprint()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	driver = coding.CheckDriverFingerprint(driver)
	t.runs.host.boundary("before_coding_check_plan")
	err = t.runs.write(live, func(tx *sql.Tx) error {
		if err := t.fence(live, tx); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(live, tx, t.claim.RunID()); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(live, `SELECT EXISTS(SELECT 1 FROM coding_check_plan WHERE invocation_id=?)`, inv.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return tool.ErrReplay
		}
		_, err := tx.ExecContext(live, `INSERT INTO coding_check_plan VALUES(?,?,?,?,?,?,?,?)`, inv.ID, revision, candidate.SHA256, scope, driver, digest(name+":"+args), limits.TimeoutSeconds, limits.OutputBytes)
		return err
	})
	if errors.Is(err, tool.ErrReplay) {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	cfg.Root = candidate.Directory
	started := time.Now()
	actual := (environment.Local{Config: cfg}).ExecuteShell(live, environment.ShellCommand{Command: request.Command, Limits: environment.ShellLimits{TimeoutSeconds: limits.TimeoutSeconds, OutputBytes: limits.OutputBytes}})
	if actual.Error != nil {
		return actual, nil
	}
	var terminal struct {
		Command   *string `json:"command"`
		Output    *string `json:"output"`
		Exit      *int    `json:"exit_code"`
		TimedOut  *bool   `json:"timed_out"`
		Truncated *bool   `json:"truncated"`
	}
	if json.Unmarshal([]byte(actual.Text), &terminal) != nil || terminal.Command == nil || *terminal.Command != request.Command || terminal.Output == nil || !utf8.ValidString(*terminal.Output) || len(*terminal.Output) > 3*limits.OutputBytes || terminal.Exit == nil || terminal.TimedOut == nil || terminal.Truncated == nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	outcome := "passed"
	if *terminal.Exit != 0 {
		outcome = "failed"
	}
	if *terminal.TimedOut || *terminal.Truncated {
		outcome = "incomplete"
	}
	receipt := coding.CheckReceipt{CandidateSHA256: candidate.SHA256, EnvironmentScope: scope, EnvironmentVerified: false, MethodRevision: revision, DriverHash: driver, Command: request.Command, Output: *terminal.Output, ExitCode: *terminal.Exit, TimedOut: *terminal.TimedOut, Truncated: *terminal.Truncated, DurationMS: time.Since(started).Milliseconds(), Outcome: outcome}
	encoded, err := json.Marshal(receipt)
	return tool.Result{Text: string(encoded)}, err
}

func (s *Store) readCodingChecks(ctx context.Context, taskID, candidate string, limit, byteLimit int) ([]coding.CheckAttempt, bool, error) {
	checks := []coding.CheckAttempt{}
	rows, err := s.db.QueryContext(ctx, `SELECT i.invocation_id,i.arguments_json,i.status,i.created_at,coalesce(p.candidate_sha256,v.candidate_sha256,''),coalesce(p.graph_revision,v.graph_revision,''),coalesce(p.driver_hash,v.driver_hash,''),coalesce(p.environment_scope,v.environment_scope,''),coalesce(i.result_json,'null'),coalesce(i.error_json,'null'),coalesce(v.plan_json,''),coalesce(v.project_sha256,''),coalesce(v.environment_sha256,''),coalesce(v.image_id,''),CASE WHEN x.invocation_id IS NULL THEN '' WHEN x.released=1 THEN 'confirmed' WHEN x.creation_observed=1 THEN 'pending' WHEN x.create_started=1 THEN 'creation_unknown' ELSE 'not_started' END FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) LEFT JOIN coding_check_plan p USING(invocation_id) LEFT JOIN coding_project_plan v USING(invocation_id) LEFT JOIN coding_project_resource x USING(invocation_id) WHERE r.task_id=? AND i.tool_name IN ('builtin__coding_check','builtin__coding_verify') ORDER BY i.created_at DESC,i.invocation_id DESC LIMIT ?`, taskID, limit+1)
	if err != nil {
		return nil, false, agent.ErrStorage
	}
	defer rows.Close()
	total := 0
	type attemptKey struct{ command, file, check string }
	seen := map[attemptKey]bool{}
	for rows.Next() {
		var c coding.CheckAttempt
		var arguments, result, failure, project string
		if err := rows.Scan(&c.InvocationID, &arguments, &c.Status, &c.AcceptedAt, &c.CandidateSHA256, &c.MethodRevision, &c.DriverHash, &c.EnvironmentScope, &result, &failure, &project, &c.ProjectSHA256, &c.EnvironmentSHA256, &c.Image, &c.ResourceCleanup); err != nil {
			return nil, false, agent.ErrStorage
		}
		var original struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal([]byte(arguments), &original)
		c.Command = original.Command
		if candidate != "" && c.CandidateSHA256 != "" {
			matches := candidate == c.CandidateSHA256
			c.CandidateMatches = &matches
		}
		c.Result, c.Error = json.RawMessage(result), json.RawMessage(failure)
		key := attemptKey{command: c.Command}
		var requested coding.ProjectRequest
		if json.Unmarshal([]byte(arguments), &requested) == nil && requested.ProjectFile != "" && requested.Plan.Check != "" {
			key = attemptKey{file: requested.ProjectFile, check: requested.Plan.Check}
			c.ProjectFile = requested.ProjectFile
		}
		if project != "" {
			var plan contract.ProjectPlan
			if rsiplugin.DecodeResult([]byte(project), &plan) != nil {
				return nil, false, agent.ErrStorage
			}
			c.Project = &plan
			c.Command = plan.Verify.Command
		}
		c.LatestAttempt = !seen[key]
		encoded, err := json.Marshal(c)
		if err != nil {
			return nil, false, agent.ErrStorage
		}
		total += len(encoded) + 1
		if len(checks) == limit || total > byteLimit {
			return checks, true, nil
		}
		seen[key] = true
		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, agent.ErrStorage
	}
	return checks, false, nil
}
