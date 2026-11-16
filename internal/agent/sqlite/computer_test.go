package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

type lifecycleFake struct {
	ensures, restores, checkpoints, destroys int
	instance                                 computer.Instance
	corrupt                                  bool
}

func (f *lifecycleFake) Ensure(ctx context.Context, r computer.Request) (computer.Instance, error) {
	if err := r.Fence(ctx); err != nil {
		return computer.Instance{}, err
	}
	f.ensures++
	f.instance = r.Instance
	return r.Instance, nil
}
func (f *lifecycleFake) Restore(ctx context.Context, r computer.Request, cp computer.Checkpoint) (computer.Instance, error) {
	if err := r.Fence(ctx); err != nil {
		return computer.Instance{}, err
	}
	if cp.ComputerID != r.Instance.ComputerID || cp.SourceEpoch >= r.Instance.Epoch {
		return computer.Instance{}, computer.ErrStale
	}
	f.restores++
	f.instance = r.Instance
	return r.Instance, nil
}
func (f *lifecycleFake) Checkpoint(ctx context.Context, r computer.Request) (computer.Checkpoint, error) {
	if err := r.Fence(ctx); err != nil {
		return computer.Checkpoint{}, err
	}
	f.checkpoints++
	hash, err := computer.Fingerprint(r.Manifest)
	if err != nil {
		return computer.Checkpoint{}, err
	}
	cp := computer.Checkpoint{ID: r.CheckpointID, ComputerID: r.Instance.ComputerID, SourceEpoch: r.Instance.Epoch, ManifestSHA256: hash}
	if f.corrupt {
		cp.ComputerID = "wrong-computer"
	}
	return cp, nil
}
func (f *lifecycleFake) Destroy(ctx context.Context, r computer.Request) error {
	if err := r.Fence(ctx); err != nil {
		return err
	}
	f.destroys++
	return nil
}

func computerClaims(t *testing.T, configure ...func(*Store, *Runs)) (*Store, *Runs, []*codeBridge, []tool.Invocation) {
	t.Helper()
	ctx := context.Background()
	defs := append(computer.Definitions("pinned-test"), codemode.Definition())
	for _, spec := range agent.ProviderTools([]tool.Spec{defs[0].Spec, defs[1].Spec, defs[2].Spec, defs[3].Spec}) {
		if spec.Name != codemode.ToolName {
			t.Fatal("internal bridge exposed to model", spec.Name)
		}
	}
	calls := []llm.ToolCall{{ID: "first", Name: codemode.ToolName, Arguments: `{"source":"return 1;"}`}, {ID: "second", Name: codemode.ToolName, Arguments: `{"source":"return 1;"}`}}
	s, r, _, e, exchange := admitPolicyRun(t, defs, calls, false, configure...)
	invs, err := e.Store.Accept(ctx, exchange, calls, e.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	var bridges []*codeBridge
	for _, inv := range invs {
		if err := e.Store.Start(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		b := &codeBridge{t: e.Store.(*tools), registry: e.Registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: inv.ID, ParentInvocationID: inv.ID, RunID: e.Store.(*tools).claim.RunID(), Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
		if err := b.claim(ctx, inv); err != nil {
			t.Fatal(err)
		}
		bridges = append(bridges, b)
	}
	return s, r, bridges, invs
}

func TestComputerIdentityRecoveryArbitrationAndFencing(t *testing.T) {
	s, r, bridges, invs := computerClaims(t)
	ctx := context.Background()
	provider := &lifecycleFake{}
	r.ConfigureComputer(provider, computer.Compatibility{ProviderVersion: "fixture"}, computer.RestorationManifest{Version: 1, ArtifactRefs: []string{"artifact:stable-fixture"}})
	first, err := bridges[0].computer().Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ComputerPreviewInstance(ctx)
	if err != nil || preview != first {
		t.Fatal("read-only preview lost the owned ready instance", preview, err)
	}
	for _, statement := range []string{
		`UPDATE computer SET phase='checkpointed',execution_id=NULL,version=version+1`,
		`UPDATE computer SET instance_epoch=instance_epoch+1,version=version+1`,
		`UPDATE computer SET compatibility_json='{}',version=version+1`,
		`UPDATE computer SET manifest_json='{}',version=version+1`,
		`UPDATE computer SET version=version+2`,
	} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatal("illegal state accepted", statement)
		}
	}
	if _, err := bridges[1].computer().Ensure(ctx); !errors.Is(err, computer.ErrBusy) {
		t.Fatal("live owner stolen", err)
	}
	if err := bridges[1].computer().Idle(ctx); err != nil || provider.checkpoints != 0 {
		t.Fatal("unrelated cell reclaimed Computer", err)
	}
	if err := bridges[0].computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM computer WHERE phase='checkpointed' AND execution_id IS NULL`) != 1 || provider.checkpoints != 1 || provider.destroys != 1 {
		t.Fatal("idle did not checkpoint/release")
	}
	second, err := bridges[1].computer().Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ComputerID != second.ComputerID || second.Epoch != first.Epoch+1 || first.NativeName == second.NativeName || provider.restores != 1 {
		t.Fatal(first, second, provider)
	}
	if err := bridges[0].computerStore().Fence(ctx); !errors.Is(err, computer.ErrStale) {
		t.Fatal("late old instance accepted", err)
	}
	if _, err := bridges[0].computer().Ensure(ctx); !errors.Is(err, computer.ErrStale) {
		t.Fatal("retired instance returned as live", err)
	}
	// Simulate Host death and recovery of the same accepted invocation. The
	// old VM is checkpointed before its replacement gets a new epoch.
	if _, err := s.db.Exec(`UPDATE code_execution_state SET claim_expires_at=0,version=version+1 WHERE execution_id=?`, bridges[1].req.ExecutionID); err != nil {
		t.Fatal(err)
	}
	recovered := &codeBridge{t: bridges[1].t, registry: bridges[1].registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: invs[1].ID, ParentInvocationID: invs[1].ID, RunID: bridges[1].req.RunID, Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
	if err := recovered.claim(ctx, invs[1]); err != nil {
		t.Fatal(err)
	}
	third, err := recovered.computer().Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third.ComputerID != first.ComputerID || third.Epoch != 3 || provider.checkpoints != 2 || provider.restores != 2 {
		t.Fatal(third, provider)
	}
	if err := bridges[1].computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old Host result accepted", err)
	}
	if err := recovered.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM computer_operation WHERE status='accepted'`) != 0 {
		t.Fatal("unfinished operation")
	}
}

func TestComputerCheckpointRejectsWrongProviderIdentity(t *testing.T) {
	s, r, b, _ := computerClaims(t)
	ctx := context.Background()
	f := &lifecycleFake{corrupt: true}
	r.ConfigureComputer(f, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	if _, err := b[0].computer().Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b[0].computer().Idle(ctx); !errors.Is(err, computer.ErrStale) && !errors.Is(err, computer.ErrIntegrity) {
		t.Fatal(err)
	}
	if f.destroys != 0 || scalar(t, s, `SELECT count(*) FROM computer WHERE checkpoint_json IS NOT NULL`) != 0 {
		t.Fatal("bad checkpoint committed")
	}
}

func TestComputerIdleRecoveryRetainsFrozenCheckpoint(t *testing.T) {
	for _, boundary := range []string{"before_checkpoint_commit", "after_checkpoint_commit", "after_destroy"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, bridges, invs := computerClaims(t)
			ctx := context.Background()
			provider := &lifecycleFake{}
			r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			first, err := bridges[0].computer().Ensure(ctx)
			if err != nil {
				t.Fatal(err)
			}
			oldStore := bridges[0].computerStore()
			frozen, err := oldStore.Begin(ctx, "idle", r.computerCompatibility, r.computerManifest)
			if err != nil {
				t.Fatal(err)
			}
			request := frozen.Request
			request.Fence = oldStore.Fence
			cp, err := provider.Checkpoint(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "before_checkpoint_commit" {
				if err := oldStore.SaveCheckpoint(ctx, frozen, cp); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "after_destroy" {
				if err := provider.Destroy(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`UPDATE code_execution_state SET claim_expires_at=0,version=version+1 WHERE execution_id=?`, bridges[0].req.ExecutionID); err != nil {
				t.Fatal(err)
			}
			recovered := &codeBridge{t: bridges[0].t, registry: bridges[0].registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: invs[0].ID, ParentInvocationID: invs[0].ID, RunID: bridges[0].req.RunID, Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
			if err := recovered.claim(ctx, invs[0]); err != nil {
				t.Fatal(err)
			}
			resumed, err := recovered.computerStore().Begin(ctx, "idle", r.computerCompatibility, r.computerManifest)
			if err != nil {
				t.Fatal(err)
			}
			beforeHash, _ := computer.Fingerprint(frozen.Request)
			afterHash, _ := computer.Fingerprint(resumed.Request)
			if beforeHash != afterHash {
				t.Fatal("recovery replaced frozen idle request/checkpoint", frozen.Request, resumed.Request)
			}
			if err := oldStore.Finish(ctx, frozen); !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal("old owner completed after takeover", err)
			}
			if err := recovered.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			status, err := s.ComputerStatus(ctx)
			if err != nil || status.Phase != "checkpointed" || status.Checkpoint == nil || *status.Checkpoint != cp {
				t.Fatal("committed checkpoint was not recovered", status, err)
			}
			next, err := bridges[1].computer().Ensure(ctx)
			if err != nil || next.ComputerID != first.ComputerID || next.Epoch != first.Epoch+1 {
				t.Fatal("fresh cell cannot restore recovered Computer", next, err)
			}
		})
	}
}

type retainedCheckpointFake struct {
	lifecycleFake
	units    map[string]computer.Checkpoint
	disposed map[computer.Instance]bool
}

func (f *retainedCheckpointFake) Checkpoint(ctx context.Context, r computer.Request) (computer.Checkpoint, error) {
	if err := r.Fence(ctx); err != nil {
		return computer.Checkpoint{}, err
	}
	if cp, ok := f.units[r.CheckpointID]; ok {
		return cp, nil
	}
	if f.disposed[r.Instance] {
		return computer.Checkpoint{}, computer.ErrStale
	}
	cp, err := f.lifecycleFake.Checkpoint(ctx, r)
	if err == nil {
		f.units[r.CheckpointID] = cp
	}
	return cp, err
}

func (f *retainedCheckpointFake) Destroy(ctx context.Context, r computer.Request) error {
	if err := f.lifecycleFake.Destroy(ctx, r); err != nil {
		return err
	}
	if _, ok := f.units[r.CheckpointID]; !ok {
		return computer.ErrIntegrity
	}
	f.disposed[r.Instance] = true
	return nil
}

func TestComputerAbandonedIdleRetainsFrozenCleanup(t *testing.T) {
	for _, boundary := range []string{"before_checkpoint_commit", "after_checkpoint_commit", "after_destroy"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, bridges, invs := computerClaims(t)
			ctx := context.Background()
			provider := &retainedCheckpointFake{units: map[string]computer.Checkpoint{}, disposed: map[computer.Instance]bool{}}
			r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			first, err := bridges[0].computer().Ensure(ctx)
			if err != nil {
				t.Fatal(err)
			}
			oldStore := bridges[0].computerStore()
			frozen, err := oldStore.Begin(ctx, "idle", r.computerCompatibility, r.computerManifest)
			if err != nil {
				t.Fatal(err)
			}
			request := frozen.Request
			request.Fence = oldStore.Fence
			cp, err := provider.Checkpoint(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if boundary != "before_checkpoint_commit" {
				if err := oldStore.SaveCheckpoint(ctx, frozen, cp); err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "after_destroy" {
				if err := provider.Destroy(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			// Storage fixture for a terminal invocation; the maintained CLI
			// harness exercises actual Main Task cancellation and Run/Code stop.
			if _, err := s.db.Exec(`UPDATE tool_invocation SET status='cancelled',version=version+1 WHERE invocation_id=?`, invs[0].ID); err != nil {
				t.Fatal(err)
			}
			plan, err := bridges[1].computerStore().Begin(ctx, "ensure", r.computerCompatibility, r.computerManifest)
			if err != nil || plan.Cleanup == nil {
				t.Fatal("new invocation lost accepted idle cleanup", plan, err)
			}
			beforeHash, _ := computer.Fingerprint(frozen.Request)
			afterHash, _ := computer.Fingerprint(*plan.Cleanup)
			if beforeHash != afterHash {
				t.Fatal("cleanup parameters changed on takeover")
			}
			if err := oldStore.Finish(ctx, frozen); !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal("old owner committed after takeover", err)
			}
			// Kill the new owner after Begin as well: reattachment must derive
			// the same cleanup from the superseded, immutable ledger entry.
			if _, err := s.db.Exec(`UPDATE code_execution_state SET claim_expires_at=0,version=version+1 WHERE execution_id=?`, invs[1].ID); err != nil {
				t.Fatal(err)
			}
			recovered := &codeBridge{t: bridges[1].t, registry: bridges[1].registry, attempt: newID(), req: bridges[1].req}
			recovered.req.ClaimToken, recovered.req.ExecutionEpoch = "", 0
			if err := recovered.claim(ctx, invs[1]); err != nil {
				t.Fatal(err)
			}
			next, err := recovered.computer().Ensure(ctx)
			if err != nil || next.ComputerID != first.ComputerID || next.Epoch != first.Epoch+1 {
				t.Fatal("fresh invocation cannot restore disposed checkpoint", next, err)
			}
			state, err := s.ComputerStatus(ctx)
			if err != nil || state.Checkpoint == nil || *state.Checkpoint != cp || !provider.disposed[first] {
				t.Fatal("original cleanup not reconciled", state, err)
			}
			if err := recovered.computer().Idle(ctx); err != nil {
				t.Fatal("new instance idle cleanup failed", err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='cancelled'`, invs[0].ID) != 1 {
				t.Fatal("terminal invocation revived")
			}
		})
	}
}

func TestComputerCannotBypassAcceptedCodeBoundary(t *testing.T) {
	_, r, b, _ := computerClaims(t)
	ctx := context.Background()
	f := &lifecycleFake{}
	r.ConfigureComputer(f, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	result, err := b[0].t.executeComputer(ctx, tool.Invocation{Call: llm.ToolCall{Name: "builtin__computer_discover", Arguments: `{"method":"getState"}`}})
	if err != nil || result.Error == nil || result.Error.Code != "PERMISSION_DENIED" || f.ensures != 0 {
		t.Fatal(result, err)
	}
	// Request drift under the frozen lifecycle operation fails closed.
	if _, err := b[0].computer().Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	r.computerManifest.ArtifactRefs = []string{"artifact:changed"}
	if _, err := b[0].computer().Ensure(ctx); !errors.Is(err, computer.ErrCompatibility) {
		t.Fatal("manifest drift accepted", err)
	}
}

func TestComputerMigrationFromPopulatedV20PreservesAcceptedWork(t *testing.T) {
	s, _, b, _ := computerClaims(t)
	ctx := context.Background()
	path := s.path
	var request string
	if err := s.db.QueryRow(`SELECT request_json FROM model_generation LIMIT 1`).Scan(&request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(removeAutomationFixtureSchema + `DROP TABLE computer_file; DROP TABLE computer_use; DROP TABLE computer_lease_resume; DROP TABLE computer_operation; DROP TABLE computer; PRAGMA user_version=20`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var after string
	if err := reopened.db.QueryRow(`SELECT request_json FROM model_generation LIMIT 1`).Scan(&after); err != nil || after != request {
		t.Fatal("accepted request changed", err)
	}
	if scalar(t, reopened, `PRAGMA user_version`) != len(migrations) || scalar(t, reopened, `SELECT count(*) FROM code_execution_state WHERE execution_id=? AND claim_token=?`, b[0].req.ExecutionID, b[0].req.ClaimToken) != 1 {
		t.Fatal("migration changed owner")
	}
	rows, err := reopened.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("broken foreign key")
	}
	// A failed migration leaves both version and existing records intact.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "prior.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(ctx, db, migrations[:20]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE computer_operation(conflict TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migrations); err == nil {
		t.Fatal("conflicting migration accepted")
	}
	var version, count int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='computer'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if version != 20 || count != 0 {
		t.Fatal("partially committed migration", version, count)
	}
}
