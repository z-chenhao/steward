package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func codingCheckFixture(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider, environment.Config) {
	t.Helper()
	s, r, a, p, cfg := codeFixture(t)
	defs := coding.CheckDefinitions(environment.ResourceHash(cfg))
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry, r.registry = registry, registry
	return s, r, a, p, cfg
}

// The protocol fixture reads the real status tool's delivered receipt; it never
// supplies a digest or terminal result on behalf of the Host.
func codingCheckProvider(t *testing.T, command string, timeout *int) func(llm.GenerationRequest) (llm.GenerationResult, error) {
	t.Helper()
	return delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "tool" {
			return calls(coding.StatusTool, `{}`), nil
		}
		var status coding.CheckStatus
		if json.Unmarshal([]byte(last.Text), &status) == nil && len(status.CandidateSHA256) == 64 && !strings.Contains(last.Text, `"outcome"`) {
			var lastCall string
			for _, message := range req.Messages {
				for _, call := range message.ToolCalls {
					lastCall = call.Name
				}
			}
			if lastCall == coding.StatusTool {
				raw, _ := json.Marshal(coding.CheckRequest{Command: command, CandidateSHA256: status.CandidateSHA256, Timeout: timeout})
				return calls(coding.CheckTool, string(raw)), nil
			}
		}
		return llm.GenerationResult{Text: "original check receipt observed"}, nil
	})
}

func checkReceipt(t *testing.T, s *Store) coding.CheckReceipt {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name='builtin__coding_check' ORDER BY created_at DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var receipt coding.CheckReceipt
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &receipt) != nil {
		t.Fatal("missing real check receipt", raw)
	}
	return receipt
}

func approveCodingCheck(t *testing.T, s *Store, r *Runs) {
	t.Helper()
	pending, err := r.PendingApprovals(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err := r.Decide(context.Background(), idAgent(t, s), pending[0].ID, pending[0].Hash, "approved"); err != nil {
		t.Fatal(err)
	}
}

func TestCodingCheckExactCandidateIndependentOutputAndRetainedReport(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	p.respond = codingCheckProvider(t, `test "$(cat file.txt)" = 'dirty source' && printf verifier > file.txt && printf receipt`, nil)
	mustSubmit(t, a, "independent-check", "Check this exact candidate independently")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 0 {
		t.Fatal("check executed without exact approval")
	}
	var ref, dir string
	if err := s.db.QueryRow(`SELECT t.task_ref,e.directory FROM task t JOIN task_code_environment e USING(task_id)`).Scan(&ref, &dir); err != nil {
		t.Fatal(err)
	}
	before, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || len(before.Checks) != 1 || before.Checks[0].Status != "waiting" || before.Checks[0].CandidateSHA256 != "" {
		t.Fatal(before, err)
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	receipt := checkReceipt(t, s)
	if receipt.CandidateSHA256 != before.CandidateSHA256 || receipt.Outcome != "passed" || receipt.ExitCode != 0 || receipt.Output != "receipt" || receipt.EnvironmentVerified || receipt.MethodRevision == "" || receipt.DriverHash == "" {
		t.Fatal("incorrect terminal facts", receipt)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(data) != "dirty source" {
		t.Fatal("verifier modified author code", string(data), err)
	}
	if paths, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "candidate-*")); len(paths) != 0 {
		t.Fatal("verifier workspace leaked", paths)
	}
	current, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || len(current.Checks) != 1 || current.Checks[0].CandidateMatches == nil || !*current.Checks[0].CandidateMatches {
		t.Fatal("receipt not tied to candidate", current, err)
	}
	artifact, err := s.SaveCodeReport(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("next candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || changed.Checks[0].CandidateMatches == nil || *changed.Checks[0].CandidateMatches || changed.Checks[0].CandidateSHA256 != receipt.CandidateSHA256 {
		t.Fatal("old pass covered new code", changed, err)
	}
	reader, err := s.Artifacts().Open(context.Background(), artifactOwner(t, s), artifact.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var saved coding.Report
	if err := json.NewDecoder(reader).Decode(&saved); err != nil || saved.CandidateSHA256 != receipt.CandidateSHA256 || saved.Checks[0].CandidateMatches == nil || !*saved.Checks[0].CandidateMatches {
		t.Fatal("historical report rebound", saved, err)
	}
	for _, mutation := range []string{`UPDATE coding_check_plan SET candidate_sha256=printf('%064d',0)`, `DELETE FROM coding_check_plan`, `INSERT OR REPLACE INTO coding_check_plan SELECT * FROM coding_check_plan`} {
		if _, err := s.db.Exec(mutation); err == nil {
			t.Fatal("accepted check plan mutable", mutation)
		}
	}
}

func TestCodingCheckApprovedCandidateDriftDoesNotRun(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	p.respond = codingCheckProvider(t, `echo MUST_NOT_RUN`, nil)
	mustSubmit(t, a, "stale-approval", "Check current source")
	mustDrain(t, a)
	var dir string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed after approval request"), 0600); err != nil {
		t.Fatal(err)
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND status='failed' AND json_extract(result_json,'$.error.code')='NON_DETERMINISTIC_REPLAY'`) != 1 {
		t.Fatal("stale approved candidate executed")
	}
}

func TestCodingCheckAuthorCanContinueWhileIndependentVerifierRuns(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	provider := codingCheckProvider(t, "touch check-started; while ! test -f check-release; do sleep 0.01; done; cat file.txt", nil)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		result, err := provider(req)
		if len(result.ToolCalls) == 1 && result.ToolCalls[0].Name == coding.CheckTool {
			result.ToolCalls = append(result.ToolCalls, llm.ToolCall{ID: "author", Name: "builtin__bash", Arguments: `{"command":"printf author-next > file.txt"}`})
		}
		return result, err
	}
	mustSubmit(t, a, "concurrent-author", "Continue authoring while checking the captured candidate")
	mustDrain(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	approvals, err := r.PendingApprovals(ctx)
	if err != nil || len(approvals) != 2 {
		t.Fatal(approvals, err)
	}
	for _, approval := range approvals {
		if err := r.Decide(ctx, idAgent(t, s), approval.ID, approval.Hash, "approved"); err != nil {
			t.Fatal(err)
		}
	}
	var run, check, author, dir, ref string
	if err := s.db.QueryRow(`SELECT i.run_id,i.invocation_id,e.directory,t.task_ref FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) JOIN task t USING(task_id) WHERE i.tool_name='builtin__coding_check'`).Scan(&run, &check, &dir, &ref); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT invocation_id FROM tool_invocation WHERE tool_name='builtin__bash'`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	claim, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(context.Background(), claim)
	store := r.ToolStore(claim)
	if err := store.Start(ctx, check); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		result tool.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: check})
		done <- completion{result, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	var verifier string
	for verifier == "" {
		paths, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "candidate-*", "check-started"))
		if len(paths) == 1 {
			verifier = filepath.Dir(paths[0])
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real verifier did not start", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := store.Start(ctx, author); err != nil {
		t.Fatal("independent verifier blocked author slot", err)
	}
	authored, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: author})
	if err != nil || authored.Error != nil {
		t.Fatal(authored, err)
	}
	if err := store.Finish(ctx, author, authored); err != nil {
		t.Fatal(err)
	}
	if bytes, err := os.ReadFile(filepath.Join(dir, "file.txt")); err != nil || string(bytes) != "author-next" {
		t.Fatal("author did not progress", string(bytes), err)
	}
	if err := os.WriteFile(filepath.Join(verifier, "check-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	completed := <-done
	joined = true
	if completed.err != nil || completed.result.Error != nil {
		t.Fatal(completed)
	}
	if err := store.Finish(ctx, check, completed.result); err != nil {
		t.Fatal(err)
	}
	if got := checkReceipt(t, s); got.Output != "dirty source" {
		t.Fatal("verifier observed moving author files", got)
	}
	report, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].CandidateMatches == nil || *report.Checks[0].CandidateMatches {
		t.Fatal("stale check covered concurrent edits", report, err)
	}
}

func TestCodingCheckFailuresTimeoutAndTruncationAreNeverPasses(t *testing.T) {
	for _, c := range []struct {
		name, command, outcome string
		timeout                *int
	}{
		{"nonzero", "printf diagnostic; exit 7", "failed", nil},
		{"timeout", "sleep 10", "incomplete", new(1)},
		{"truncated", "printf '%060000d' 0", "incomplete", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, r, a, p, _ := codingCheckFixture(t)
			installCodingPlugin(t, s, r)
			p.respond = codingCheckProvider(t, c.command, c.timeout)
			mustSubmit(t, a, "terminal-facts", "Run bounded verification")
			mustDrain(t, a)
			approveCodingCheck(t, s, r)
			mustDrain(t, a)
			receipt := checkReceipt(t, s)
			if receipt.Outcome != c.outcome {
				t.Fatal(receipt)
			}
			if c.name == "nonzero" && (receipt.ExitCode != 7 || receipt.Output != "diagnostic") || c.name == "timeout" && !receipt.TimedOut || c.name == "truncated" && !receipt.Truncated {
				t.Fatal("original diagnostics lost", receipt)
			}
		})
	}
}

func TestCodingCheckRequiresManagedServiceWithoutNativeFallback(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	for _, spec := range agent.MainTools(a.Registry.Describe()) {
		if coding.IsCheckTool(spec.Name) {
			t.Fatal("Main acquired Worker execution capability", spec.Name)
		}
	}
	for _, specs := range [][]tool.Spec{agent.WorkerTools(a.Registry.Describe()), agent.ChildTools(a.Registry.Describe())} {
		seen := 0
		for _, spec := range specs {
			if coding.IsCheckTool(spec.Name) {
				seen++
			}
		}
		if seen != 2 {
			t.Fatal("Root/Child lost check capabilities")
		}
	}
	p.respond = codingCheckProvider(t, "printf MUST_NOT_RUN", nil)
	mustSubmit(t, a, "no-method", "Run independent check")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
		t.Fatal("unmanaged check silently dispatched")
	}
}

func TestCodingCheckLatestAttemptDoesNotHideEarlierOutcome(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	base := codingCheckProvider(t, "sleep 2; printf result", nil)
	second := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		var receipt coding.CheckReceipt
		if isRoot(req) && last.Role == "tool" && json.Unmarshal([]byte(last.Text), &receipt) == nil && receipt.Outcome == "passed" && !second {
			second = true
			raw, _ := json.Marshal(coding.CheckRequest{Command: receipt.Command, CandidateSHA256: receipt.CandidateSHA256, Timeout: new(1)})
			return calls(coding.CheckTool, string(raw)), nil
		}
		return base(req)
	}
	mustSubmit(t, a, "newer-attempt", "Inspect latest attempt separately from history")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	report, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || len(report.Checks) != 2 || !report.Checks[0].LatestAttempt || report.Checks[0].Status != "waiting" || report.Checks[1].LatestAttempt || report.Checks[1].Status != "succeeded" {
		t.Fatal("prior pass hid a newer pending check", report, err)
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if got := checkReceipt(t, s); got.Outcome != "incomplete" || !got.TimedOut {
		t.Fatal("newer timeout became prior pass", got)
	}
	report, err = s.CodeReport(context.Background(), ref, true)
	if err != nil || len(report.Checks) != 2 || !report.Checks[0].LatestAttempt || report.Checks[1].LatestAttempt {
		t.Fatal("attempt history lost", report, err)
	}
}

func TestCodingCheckInvalidAttemptsRemainInspectableAndBudgeted(t *testing.T) {
	s, _, a, p, _ := codingCheckFixture(t)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "invalid arguments observed"}, nil
		}
		return calls(coding.CheckTool, `{}`, `{"command":7}`), nil
	})
	mustSubmit(t, a, "invalid-check", "Inspect failed attempts")
	mustDrain(t, a)
	var taskID, ref string
	if err := s.db.QueryRow(`SELECT task_id,task_ref FROM task`).Scan(&taskID, &ref); err != nil {
		t.Fatal(err)
	}
	report, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || len(report.Checks) != 2 || report.ChecksTruncated {
		t.Fatal("invalid calls broke report inspection", report, err)
	}
	for _, check := range report.Checks {
		if check.Status != "failed" || check.CandidateSHA256 != "" || check.CandidateMatches != nil {
			t.Fatal("rejected request became an accepted check", check)
		}
	}
	checks, truncated, err := s.readCodingChecks(context.Background(), taskID, report.CandidateSHA256, 1, 1<<20)
	if err != nil || len(checks) != 1 || !truncated {
		t.Fatal("count truncation missing", checks, truncated, err)
	}
	checks, truncated, err = s.readCodingChecks(context.Background(), taskID, report.CandidateSHA256, 64, 1)
	if err != nil || len(checks) != 0 || !truncated {
		t.Fatal("byte truncation missing", checks, truncated, err)
	}
}

func TestCodingCheckFrozenMethodIndependentUpgradeRollbackAndCeilings(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	h := installCodingPlugin(t, s, r)
	ctx := context.Background()
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.respond = codingCheckProvider(t, "printf '%02048d' 0", nil)
	mustSubmit(t, a, "retained-check", "Check using frozen method")
	mustDrain(t, a)
	second, _ := selectCodingVersion(t, h, first.Revision, "1.1.0")
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	retained := checkReceipt(t, s)
	if retained.MethodRevision != first.Revision || len(retained.Output) != 2048 || retained.Truncated {
		t.Fatal("upgrade rebound already approved method", retained)
	}
	mustSubmit(t, a, "new-check", "Check using selected method")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	upgraded := checkReceipt(t, s)
	if upgraded.MethodRevision != second || !upgraded.Truncated || upgraded.Outcome != "incomplete" || len(upgraded.Output) != 1024 {
		t.Fatal("independent method upgrade not used", upgraded)
	}
	activateDelegation(t, h, "check-rollback", second, first.Revision, "coding-owner-fixture")
	mustSubmit(t, a, "rollback-check", "Check after rollback")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if got := checkReceipt(t, s); got.MethodRevision != first.Revision || got.Truncated || len(got.Output) != 2048 {
		t.Fatal("rollback did not restore selected method", got)
	}
	selectCodingVersion(t, h, first.Revision, "1.6.0")
	mustSubmit(t, a, "excess-check", "Reject excessive checker limits")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 3 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
		t.Fatal("package expanded trusted driver ceilings")
	}
}

func TestCodingCheckLateForgetAndGuardianRejectPlanBeforeCommand(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			s, r, a, p, _ := codingCheckFixture(t)
			h := installCodingPlugin(t, s, r)
			ctx := context.Background()
			selected, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p.respond = codingCheckProvider(t, "printf PRIVATE_CHECK_CANARY", nil)
			mustSubmit(t, a, "check-revocation", "Check approved candidate")
			mustDrain(t, a)
			approveCodingCheck(t, s, r)
			var run, invocation string
			if err := s.db.QueryRow(`SELECT run_id,invocation_id FROM tool_invocation WHERE tool_name='builtin__coding_check'`).Scan(&run, &invocation); err != nil {
				t.Fatal(err)
			}
			claim, err := r.Claim(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Release(ctx, claim)
			store := r.ToolStore(claim)
			if err := store.Start(ctx, invocation); err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			revoked := false
			s.MainBoundary = func(point string) {
				if point != "before_coding_check_plan" {
					return
				}
				revoked = true
				var err error
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				case "stop":
					err = h.Store.Stop(ctx, true)
				case "quarantine":
					err = h.Store.Quarantine(ctx, selected.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			result, executeErr := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: invocation})
			if !revoked || executeErr == nil || result.Text != "" || scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 0 {
				t.Fatal("revoked check accepted choice or terminal data", result, executeErr)
			}
			if cause == "forget" && !errors.Is(executeErr, agent.ErrClaimLost) {
				t.Fatal("privacy loss cause lost", executeErr)
			}
		})
	}
}

func TestCodingCheckRunningForgetCancelsTransportAndRejectsPrivateReceipt(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	p.respond = codingCheckProvider(t, "touch check-started; while true; do sleep 0.01; done; printf PRIVATE_CHECK_CANARY", nil)
	mustSubmit(t, a, "running-forget", "Check this candidate")
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var run, invocation, dir string
	if err := s.db.QueryRow(`SELECT i.run_id,i.invocation_id,e.directory FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) WHERE i.tool_name='builtin__coding_check'`).Scan(&run, &invocation, &dir); err != nil {
		t.Fatal(err)
	}
	claim, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(context.Background(), claim)
	store := r.ToolStore(claim)
	if err := store.Start(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		result tool.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: invocation})
		done <- completion{result, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	for {
		paths, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "candidate-*", "check-started"))
		if len(paths) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real verifier never started", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	owner := idAgent(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-done:
		joined = true
		if !errors.Is(completed.err, agent.ErrClaimLost) || completed.result.Text != "" {
			t.Fatal("private verifier survived Forget or returned output", completed)
		}
	case <-ctx.Done():
		t.Fatal("revoked real process was not cancelled", ctx.Err())
	}
	if paths, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "candidate-*")); len(paths) != 0 {
		t.Fatal("revoked candidate retained", paths)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND status='succeeded'`) != 0 {
		t.Fatal("revoked check became passed")
	}
}

func TestCodingCheckRealCodeModeUsesOneApprovalAndOriginalReceipt(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	defs := []tool.Definition{codemode.Definition()}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry, r.registry = registry, registry
	r.ConfigureJavaScript(realCodeRuntime(t))
	installCodingPlugin(t, s, r)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "check receipt observed"}, nil
		}
		return calls(codemode.ToolName, `{"source":"const s=await tools.builtin__coding_status({}); return await tools.builtin__coding_check({candidate_sha256:s.candidate_sha256,command:'printf original-receipt'});"}`), nil
	})
	mustSubmit(t, a, "check-cell", "Check exact source via Code Mode")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 0 {
		t.Fatal("Code Mode bypassed approval")
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND status='succeeded' AND attempt_count=1`) != 1 || checkReceipt(t, s).Output != "original-receipt" {
		t.Fatal("cell repeated or lost check")
	}
}

func TestCodingCheckPlanAndReceiptCommitFailuresStayUnknownWithoutReplay(t *testing.T) {
	for _, boundary := range []string{"plan", "receipt"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, a, p, _ := codingCheckFixture(t)
			installCodingPlugin(t, s, r)
			p.respond = codingCheckProvider(t, "printf checked", nil)
			mustSubmit(t, a, "check-crash", "Run check once")
			mustDrain(t, a)
			trigger := `CREATE TRIGGER reject_check BEFORE INSERT ON coding_check_plan BEGIN SELECT RAISE(ABORT,'injected plan failure'); END`
			if boundary == "receipt" {
				trigger = `CREATE TRIGGER reject_check BEFORE UPDATE ON tool_invocation WHEN OLD.tool_name='builtin__coding_check' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`
			}
			if _, err := s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			approveCodingCheck(t, s, r)
			if err := a.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
				t.Fatal("commit boundary not exercised", err)
			}
			expected := 0
			if boundary == "receipt" {
				expected = 1
			}
			if scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != expected {
				t.Fatal("incorrect plan acceptance")
			}
			if _, err := s.db.Exec(`DROP TRIGGER reject_check`); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_check' AND status='unknown' AND attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_check_plan`) != expected {
				t.Fatal("unknown check replayed or became success")
			}
		})
	}
}
