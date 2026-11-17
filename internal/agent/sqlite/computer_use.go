package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/tool"
)

type computerUseStore struct {
	b       *codeBridge
	version int64
}

func (s *computerUseStore) Fence(ctx context.Context) error {
	if err := s.b.computerStore().Fence(ctx); err != nil {
		return err
	}
	return s.b.t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return s.fence(ctx, tx) })
}
func (s *computerUseStore) fence(ctx context.Context, tx *sql.Tx) error {
	if err := s.b.fence(ctx, tx); err != nil {
		return err
	}
	return s.b.t.runs.pluginDispatchFence(ctx, tx, s.b.t.claim.RunID())
}
func (s *computerUseStore) Load(ctx context.Context) (computer.UseState, error) {
	var state computer.UseState
	err := s.b.t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := s.fence(ctx, tx); err != nil {
			return err
		}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT state_json,version FROM computer_use WHERE execution_id=?`, s.b.req.ExecutionID).Scan(&raw, &s.version)
		if err == sql.ErrNoRows {
			s.version = 0
			err = nil
		}
		if err != nil {
			return err
		}
		if raw != "" {
			if err = json.Unmarshal([]byte(raw), &state); err != nil {
				return err
			}
		}
		state.Files = map[string]bool{}
		scope, err := runScope(ctx, tx, s.b.t.claim.RunID())
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT f.path FROM computer_file f JOIN computer c USING(computer_id) WHERE f.agent_id=? AND (f.run_id=? OR (?!='' AND f.task_id=?))`, scope.AgentID, scope.RunID, scope.TaskID, scope.TaskID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			if err = rows.Scan(&path); err != nil {
				return err
			}
			state.Files[path] = true
		}
		return rows.Err()
	})
	return state, err
}
func (s *computerUseStore) Save(ctx context.Context, state computer.UseState) error {
	err := s.b.t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := s.fence(ctx, tx); err != nil {
			return err
		}
		raw, err := encoded(state)
		if err != nil {
			return err
		}
		var res sql.Result
		if s.version == 0 {
			res, err = tx.ExecContext(ctx, `INSERT INTO computer_use VALUES(?,?,1) ON CONFLICT DO NOTHING`, s.b.req.ExecutionID, raw)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE computer_use SET state_json=?,version=version+1 WHERE execution_id=? AND version=?`, raw, s.b.req.ExecutionID, s.version)
		}
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			return computer.ErrStale
		}
		if len(state.Files) > 0 {
			scope, err := runScope(ctx, tx, s.b.t.claim.RunID())
			if err != nil {
				return err
			}
			for path := range state.Files {
				if !computer.SafeTransferPath(path) {
					return computer.ErrInvalid
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO computer_file SELECT computer_id,?,?,?,? FROM computer WHERE singleton=1 ON CONFLICT DO NOTHING`, path, scope.AgentID, nullable(scope.TaskID), scope.RunID); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err == nil {
		s.version++
	}
	return err
}

func (s *computerUseStore) OpenArtifact(ctx context.Context, ref string) (io.ReadCloser, error) {
	file, _, err := s.b.t.runs.openArtifact(ctx, ref, func(tx *sql.Tx) (artifact.Scope, error) {
		if err := s.b.fence(ctx, tx); err != nil {
			return artifact.Scope{}, err
		}
		return runScope(ctx, tx, s.b.t.claim.RunID())
	})
	return file, err
}
func (t *tools) executeComputerUse(ctx context.Context, inv tool.Invocation) (result tool.Result, err error) {
	b := t.codeOwner
	project, err := t.computerObservationProjector(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	wait, err := t.computerWaitPolicy(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	documentation, err := t.computerDocumentationReader(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	selection, err := t.computerSelectionPlanner(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	order, err := t.computerInventoryOrderer(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	matcher, err := t.computerTargetMatcher(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	parameters, err := t.computerActionParameterReader(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	upload, err := t.computerBrowserUploader(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	snapshot, browserProject, err := t.computerBrowserProjection(ctx, inv)
	if err != nil {
		return environmentMethodFailure(err)
	}
	instance, err := b.computer().Ensure(ctx)
	if err != nil {
		if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
			return tool.Result{}, err
		}
		return computerFailure(err), nil
	}
	t.runs.host.boundary("computer_ready")
	driver, ok := t.runs.computerProvider.(computer.Driver)
	if !ok { // The lifecycle-only provider remains an explicit contract fixture.
		raw := `{"computerId":` + strconv.Quote(instance.ComputerID) + `,"instanceEpoch":` + strconv.Quote(fmt.Sprint(instance.Epoch)) + `,"platform":"macos","apps":[],"windows":[],"browsers":[],"errors":[{"code":"PROVIDER_UNAVAILABLE"}]}`
		return tool.Result{Text: raw}, nil
	}
	store := &computerUseStore{b: b}
	// Loss of a persisted Run/Code lease cancels the SSH/Browser transport while
	// still in flight. Keep this watcher joined before returning to the executor.
	live, stop := context.WithCancel(ctx)
	joined := make(chan struct{})
	done := make(chan struct{})
	var fenceErr error
	go func() {
		defer close(done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-joined:
				return
			case <-live.Done():
				return
			case <-tick.C:
				if err := store.Fence(live); err != nil {
					if live.Err() == nil {
						fenceErr = err
					}
					stop()
					return
				}
			}
		}
	}()
	defer func() {
		close(joined)
		stop()
		<-done
		if fenceErr != nil {
			result, err = environmentMethodFailure(fenceErr)
		}
	}()
	runtime := computer.UseRuntime{Driver: driver, Store: store, Instance: instance, Lease: b.req.ExecutionID, ProjectObservation: project, WaitPolicy: wait, ReadDocumentation: documentation, PlanSelection: selection, OrderInventory: order, MatchTargets: matcher, ReadActionParameters: parameters, UploadBrowser: upload, SnapshotBrowser: snapshot, ProjectBrowser: browserProject}
	result, err = runtime.Call(live, inv.Call.Name, inv.Call.Arguments)
	if err != nil {
		if errors.Is(err, computer.ErrInvalid) {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		return environmentMethodFailure(err)
	}
	if err = store.Fence(ctx); err != nil {
		return tool.Result{}, err
	}
	return result, nil
}

// commitObservation makes references actionable only when their Tool result and
// any image Artifact commit. Failed normalization leaves the pending view inert.
func (t *tools) commitObservation(ctx context.Context, tx *sql.Tx, receipt computer.ObservationReceipt) error {
	if t.codeOwner == nil || receipt.Lease != t.codeOwner.req.ExecutionID {
		return agent.ErrClaimLost
	}
	if err := t.codeOwner.fence(ctx, tx); err != nil {
		return err
	}
	if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
		return err
	}
	var raw string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT state_json,version FROM computer_use WHERE execution_id=?`, receipt.Lease).Scan(&raw, &version); err != nil {
		return err
	}
	var state computer.UseState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	h := state.Handles[receipt.Token]
	if h == nil || h.Lease != receipt.Lease || h.AXRevision != receipt.AXRevision || h.ScreenshotRevision != receipt.ScreenshotRevision {
		return agent.ErrConflict
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer WHERE execution_id=? AND execution_epoch=? AND computer_id=? AND instance_epoch=? AND phase='ready')`, receipt.Lease, t.codeOwner.req.ExecutionEpoch, h.Instance.ComputerID, h.Instance.Epoch).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return agent.ErrClaimLost
	}
	if receipt.UpdateAX {
		h.AXReady = true
	}
	if receipt.UpdateScreenshot {
		h.ScreenshotReady = true
	}
	raw, err := encoded(state)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE computer_use SET state_json=?,version=version+1 WHERE execution_id=? AND version=?`, raw, receipt.Lease, version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return agent.ErrConflict
	}
	return nil
}
