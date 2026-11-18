package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/sys/unix"
)

func projectDocument(directory, name string) ([]byte, error) {
	if !contract.ValidProjectPath(name) {
		return nil, coding.ErrInvalid
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32768 {
		return nil, coding.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, 32769))
	if err != nil {
		return nil, err
	}
	if len(raw) > 32768 || !utf8.Valid(raw) {
		return nil, coding.ErrInvalid
	}
	return raw, nil
}

func validProjectPlan(plan contract.ProjectPlan) bool {
	raw, err := json.Marshal(plan)
	if err != nil || !tool.Validate(contract.ProjectPlanSchema, string(raw)) {
		return false
	}
	return contract.ValidEvidencePaths(plan.Evidence) && plan.Prepare.TimeoutSeconds+plan.Doctor.TimeoutSeconds+plan.Verify.TimeoutSeconds+plan.Cleanup.TimeoutSeconds <= 240
}

func (t *tools) projectSelection(ctx context.Context, candidate, baseline, file, check string) (coding.ProjectSelection, string, error) {
	selected := coding.ProjectSelection{File: file}
	raw, err := projectDocument(candidate, file)
	if err != nil {
		selected.ErrorCode = "PROVIDER_UNAVAILABLE"
		if errors.Is(err, os.ErrNotExist) {
			selected.ErrorCode = "NOT_FOUND"
		}
		if errors.Is(err, coding.ErrInvalid) {
			selected.ErrorCode = "INVALID_ARGUMENT"
		}
		return selected, "", nil
	}
	selected.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	before, err := projectDocument(baseline, file)
	selected.DocumentChanged = err != nil || fmt.Sprintf("%x", sha256.Sum256(before)) != selected.SHA256
	if t.runs.plugins == nil {
		return selected, "", rsi.ErrNotFound
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return selected, "", err
	}
	if revision == "" {
		return selected, "", rsi.ErrNotFound
	}
	input, _ := json.Marshal(contract.ProjectFacts{Document: string(raw), Check: check})
	result, err := h.ReadService(ctx, revision, contract.ProjectContract, input, nil)
	if err != nil {
		return selected, "", err
	}
	var proposal contract.ProjectProposal
	if rsiplugin.DecodeResult(result, &proposal) != nil {
		return selected, "", rsi.ErrInvalid
	}
	if proposal.Plan == nil {
		if proposal.ErrorCode != "INVALID_ARGUMENT" && proposal.ErrorCode != "NOT_FOUND" {
			return selected, "", rsi.ErrInvalid
		}
		selected.ErrorCode = proposal.ErrorCode
		return selected, revision, nil
	}
	if proposal.ErrorCode != "" || !validProjectPlan(*proposal.Plan) {
		return selected, "", rsi.ErrInvalid
	}
	if t.runs.localEnvironment != nil && t.runs.localEnvironment.ProjectContainer != nil {
		selected.Environment = t.runs.localEnvironment.ProjectContainer.Image
	}
	selected.Plan = proposal.Plan
	return selected, revision, nil
}

func (t *tools) executeProjectVerification(ctx context.Context, inv tool.Invocation, candidate *coding.CandidateSnapshot, cfg environment.Config, taskID, scope, args string) (tool.Result, error) {
	var request coding.ProjectRequest
	if rsiplugin.Decode([]byte(args), &request) != nil || !validProjectPlan(request.Plan) {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if cfg.ProjectContainer == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if candidate.SHA256 != request.CandidateSHA256 || request.Environment != cfg.ProjectContainer.Image {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	selection, revision, err := t.projectSelection(ctx, candidate.Directory, coding.Location(t.runs.host.codeRoot, taskID).Baseline, request.ProjectFile, request.Plan.Check)
	if err != nil {
		return environmentMethodFailure(err)
	}
	if selection.ErrorCode != "" {
		return tool.Failure(selection.ErrorCode), nil
	}
	approved, _ := json.Marshal(request.Plan)
	accepted, _ := json.Marshal(selection.Plan)
	if selection.SHA256 != request.ProjectSHA256 || string(approved) != string(accepted) {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	client, err := projectcontainer.New(*cfg.ProjectContainer)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	defer client.Close()
	basis, err := client.Basis(ctx)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	environmentHash := basis.Digest()
	if request.EnvironmentSHA256 != environmentHash {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	basisJSON, _ := json.Marshal(basis)
	driver := coding.ProjectDriverFingerprint(projectcontainer.DriverFingerprint())
	t.runs.host.boundary("before_coding_project_plan")
	repeated := false
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		var image, saved string
		err := tx.QueryRowContext(ctx, `SELECT image_id,environment_sha256 FROM coding_task_environment WHERE task_id=?`, taskID).Scan(&image, &saved)
		if err == nil {
			if image != basis.Image || saved != environmentHash {
				return tool.ErrReplay
			}
		} else if err == sql.ErrNoRows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO coding_task_environment VALUES(?,?,?,?)`, taskID, basis.Image, environmentHash, string(basisJSON)); err != nil {
				return err
			}
		} else {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coding_project_plan WHERE invocation_id=?)`, inv.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			repeated = true
			return tool.ErrReplay
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO coding_project_plan VALUES(?,?,?,?,?,?,?,?,?,?)`, inv.ID, revision, candidate.SHA256, scope, environmentHash, basis.Image, driver, digest(coding.VerifyTool+":"+args), selection.SHA256, string(approved)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO coding_project_resource(invocation_id,socket_path,directory) VALUES(?,?,?)`, inv.ID, cfg.ProjectContainer.Socket, candidate.Directory)
		return err
	})
	if errors.Is(err, tool.ErrReplay) {
		if repeated {
			return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
		}
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	commands := projectcontainer.Commands{
		Prepare: projectcontainer.Step(request.Plan.Prepare),
		Start:   (*projectcontainer.Step)(request.Plan.Start),
		Doctor:  projectcontainer.Step(request.Plan.Doctor),
		Verify:  projectcontainer.Step(request.Plan.Verify),
		Cleanup: projectcontainer.Step(request.Plan.Cleanup),
	}
	t.runs.host.boundary("after_coding_project_plan")
	if err := t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE coding_project_resource SET create_started=1 WHERE invocation_id=? AND released=0`, inv.ID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return tool.ErrReplay
		}
		return nil
	}); err != nil {
		return tool.Result{}, err
	}
	created := func(context.Context) error {
		// This is a trusted observation of this exact create, independent of
		// authority to continue project commands or publish their result.
		fact, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return t.runs.host.Transaction(fact, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(fact, `UPDATE coding_project_resource SET creation_observed=1 WHERE invocation_id=?`, inv.ID)
			return err
		})
	}
	terminal, runErr := client.Run(ctx, inv.ID, candidate.Directory, commands, created)
	if !terminal.CleanupConfirmed {
		candidate.Retain()
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	var evidence []coding.ProjectEvidence
	var attachments []tool.AttachmentOutput
	var evidenceFailures []coding.EvidenceFailure
	if runErr == nil && len(request.Plan.Evidence) > 0 {
		t.runs.host.boundary("before_coding_project_evidence")
		if err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
			if err := t.fence(ctx, tx); err != nil {
				return err
			}
			return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
		}); err != nil {
			return tool.Result{}, err
		}
		var err error
		evidence, attachments, evidenceFailures, err = coding.CaptureEvidence(ctx, candidate.Directory, request.Plan.Evidence)
		if err != nil {
			return tool.Result{}, err
		}
	}
	if err := candidate.Close(); err != nil {
		candidate.Retain()
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	if err := t.runs.host.Transaction(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.WithoutCancel(ctx), `UPDATE coding_project_resource SET released=1 WHERE invocation_id=?`, inv.ID)
		return err
	}); err != nil {
		return tool.Result{}, err
	}
	if runErr != nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	if !validProjectTerminal(request.Plan, terminal.Outcome, terminal.Phases) {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	if err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
	}); err != nil {
		return tool.Result{}, err
	}
	if terminal.Outcome == "passed" && len(evidenceFailures) > 0 {
		terminal.Outcome = "incomplete"
	}
	receipt := coding.ProjectReceipt{Evidence: evidence, EvidenceFailures: evidenceFailures, CandidateSHA256: candidate.SHA256, Image: basis.Image, EnvironmentSHA256: environmentHash, Runtime: basis, ProjectSHA256: selection.SHA256, DocumentChanged: selection.DocumentChanged, MethodRevision: revision, DriverHash: driver, Plan: request.Plan, Outcome: terminal.Outcome, Phases: terminal.Phases}
	raw, err := json.Marshal(receipt)
	return tool.Result{Text: string(raw), Attachments: attachments}, err
}

func validProjectTerminal(plan contract.ProjectPlan, outcome string, phases []projectcontainer.Phase) bool {
	expected := map[string]contract.ProjectStep{"prepare": plan.Prepare, "doctor": plan.Doctor, "verify": plan.Verify, "cleanup": plan.Cleanup}
	if plan.Start != nil {
		expected["start"] = *plan.Start
	}
	if len(phases) < 2 || len(phases) > len(expected) {
		return false
	}
	seen := map[string]projectcontainer.Phase{}
	last := -1
	order := map[string]int{"prepare": 0, "start": 1, "doctor": 2, "verify": 3, "cleanup": 4}
	for _, phase := range phases {
		step, exists := expected[phase.Phase]
		stopped := phase.Phase == "start" && phase.Status == "stopped"
		unavailable := phase.Status == "unavailable" && phase.ErrorCode == "PROVIDER_UNAVAILABLE" && phase.ExitCode == nil
		if !exists || phase.Command != step.Command || order[phase.Phase] <= last || phase.ExitCode == nil && !unavailable && !stopped || phase.DurationMS < 0 || len(phase.Output) > 3*48000 || !utf8.ValidString(phase.Output) {
			return false
		}
		last = order[phase.Phase]
		seen[phase.Phase] = phase
	}
	if _, ok := seen["prepare"]; !ok {
		return false
	}
	if _, ok := seen["cleanup"]; !ok {
		return false
	}
	valid := map[string]bool{"passed": true, "setup_failed": true, "verification_failed": true, "cleanup_failed": true, "incomplete": true}
	if !valid[outcome] {
		return false
	}
	if outcome == "passed" {
		for _, name := range []string{"prepare", "doctor", "verify", "cleanup"} {
			phase, ok := seen[name]
			if !ok || phase.Status != "passed" || phase.ExitCode == nil || phase.ErrorCode != "" || *phase.ExitCode != 0 || phase.TimedOut || phase.Truncated {
				return false
			}
		}
		if plan.Start != nil {
			phase, ok := seen["start"]
			if !ok || phase.Status != "stopped" || phase.TimedOut || phase.Truncated {
				return false
			}
		}
	}
	return true
}
