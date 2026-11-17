package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"slices"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	filecontract "github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
	shellcontract "github.com/z-chenhao/assistant/internal/plugin/shellpolicy/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"golang.org/x/sys/unix"
)

func (r *Runs) ConfigureEnvironment(execute func(context.Context, string, string) tool.Result) {
	r.environmentExecute = execute
}

func (r *Runs) ConfigureLocalEnvironment(cfg environment.Config) {
	r.localEnvironment = &cfg
	r.environmentExecute = (environment.Local{Config: cfg}).Execute
	r.localExecute = func(ctx context.Context, configuration environment.Config, name, args string) tool.Result {
		return (environment.Local{Config: configuration}).Execute(ctx, name, args)
	}
}

// Freeze environment facts once, after Task workspace preparation and before
// the first model exchange. Never copy Parent runtime messages into a Child.
func (r *Runs) initializeWorkerEnvironment(ctx context.Context, c agent.RunClaim, run agent.Run, delegation *delegationPolicy) error {
	cwd, local := "", false
	for _, spec := range run.Tools {
		if !environment.IsTool(spec.Name) {
			continue
		}
		if r.computerProvider != nil {
			if driver, ok := r.computerProvider.(environment.GuestDriver); ok {
				cwd = driver.WorkingDirectory()
			}
		} else if r.localEnvironment != nil {
			cwd, local = r.localEnvironment.Root, true
			err := r.host.db.QueryRowContext(ctx, `SELECT e.directory FROM task_code_environment e JOIN run r USING(task_id) WHERE r.run_id=?`, c.RunID()).Scan(&cwd)
			if err != nil && err != sql.ErrNoRows {
				return agent.ErrStorage
			}
		}
		break
	}
	if cwd != "" {
		facts := struct {
			CWD  string   `json:"cwd"`
			Docs []string `json:"docs,omitempty"`
		}{CWD: cwd}
		// Only list readable regular files in the actual local workspace.
		// Guest files are not inspected through the Host filesystem.
		if local {
			if root, err := os.OpenRoot(cwd); err == nil {
				defer root.Close()
				for _, name := range []string{"AGENTS.md", "README.md"} {
					info, err := root.Lstat(name)
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					if file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0); err == nil {
						if current, err := file.Stat(); err == nil && current.Mode().IsRegular() {
							facts.Docs = append(facts.Docs, name)
						}
						file.Close()
					}
				}
			}
		}
		run.Checkpoint.Messages = slices.Insert(run.Checkpoint.Messages, 1, agent.RuntimeContext("execution_environment", "", true, facts))
	}
	run.Checkpoint.ContextReady = true
	return r.checkpointWorkerInitialization(ctx, c, run, delegation)
}

func (t *tools) executeEnvironment(ctx context.Context, inv tool.Invocation) (result tool.Result, executionErr error) {
	if err := t.runs.write(ctx, func(tx *sql.Tx) error {
		_, name, arguments, err := t.acceptedBuiltin(ctx, tx, inv)
		if err == nil {
			inv.Call.Name, inv.Call.Arguments = name, arguments
		}
		return err
	}); err != nil {
		return tool.Result{}, err
	}
	managedFile := false
	managedShell := false
	if environment.IsFileTool(inv.Call.Name) {
		var err error
		managedFile, err = t.runs.managedRunService(ctx, t.claim.RunID(), filecontract.WindowContract)
		if err != nil {
			return tool.Result{}, err
		}
	}
	if inv.Call.Name == "builtin__bash" {
		var err error
		managedShell, err = t.runs.managedRunService(ctx, t.claim.RunID(), shellcontract.Contract)
		if err != nil {
			return tool.Result{}, err
		}
	}
	driver, ok := t.runs.computerProvider.(environment.GuestDriver)
	if t.runs.computerProvider != nil && !ok {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	b := t.codeOwner
	if b == nil && t.runs.computerProvider != nil {
		// Reuse the existing durable execution claim/lifecycle ledger for native
		// code and file calls too. There is no generated JavaScript, nested Tool
		// call, second approval, or second invocation lifecycle.
		b = &codeBridge{t: t, req: codemode.ExecutionRequest{ExecutionID: inv.ID, ParentInvocationID: inv.ID, RunID: t.claim.RunID(), Source: inv.Call.Name + ":" + inv.Call.Arguments, SourceHash: digest(inv.Call.Name + ":" + inv.Call.Arguments), RuntimeVersion: "steward-native-v1", PreludeVersion: "none"}}
		if err := b.claim(ctx, inv); err != nil {
			return tool.Result{}, err
		}
		defer func() {
			result.HostFence = codeClaimFence{b.req.ClaimToken, b.req.ExecutionEpoch}
			if executionErr != nil {
				executionErr = errors.Join(executionErr, b.abandon())
			}
		}()
	}
	// Stop transports when either the Run claim or a containing Code claim is
	// lost. Cancellation interrupts the adapter; possible effects stay unknown.
	live, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	transportFailure := make(chan error, 1)
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
					if b != nil {
						return b.fence(live, tx)
					}
					return t.fence(live, tx)
				})
				if err != nil {
					// Preserve the authority cause before cancellation kills a
					// pure method's process. Normal caller/cleanup cancellation
					// must not invent a storage failure after a successful call.
					if live.Err() != nil {
						return
					}
					transportFailure <- err
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-done
		if managedFile || managedShell {
			select {
			case cause := <-transportFailure:
				if result.Output != nil {
					if closer, ok := result.Output.Reader.(io.Closer); ok {
						_ = closer.Close()
					}
				}
				result = tool.Result{}
				executionErr = cause
			default:
			}
		}
	}()
	environmentTools := t
	if b != nil {
		environmentTools = t.forCode(b)
	}
	if t.runs.computerProvider == nil {
		if t.runs.localEnvironment != nil {
			cfg := *t.runs.localEnvironment
			var taskID string
			if err := t.runs.host.db.QueryRowContext(live, `SELECT coalesce(task_id,'') FROM run WHERE run_id=?`, t.claim.RunID()).Scan(&taskID); err != nil {
				return tool.Result{}, agent.ErrStorage
			}
			if taskID != "" {
				var directory, scope, status string
				err := t.runs.host.db.QueryRowContext(live, `SELECT directory,scope_hash,status FROM task_code_environment WHERE task_id=?`, taskID).Scan(&directory, &scope, &status)
				if err != nil && err != sql.ErrNoRows {
					return tool.Result{}, agent.ErrStorage
				}
				if err == nil {
					if scope != environment.ResourceHash(cfg) || status != "ready" {
						return tool.Failure("PERMISSION_DENIED"), nil
					}
					if directory != coding.Location(t.runs.host.codeRoot, taskID).Directory {
						return tool.Failure("PERMISSION_DENIED"), nil
					}
					info, err := os.Lstat(directory)
					if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
						return tool.Failure("PROVIDER_UNAVAILABLE"), nil
					}
					cfg.Root = directory
					if managedFile {
						return environmentTools.executeManagedFile(live, inv, localEnvironmentSession{Local: environment.Local{Config: cfg}, scope: environment.ResourceHash(*t.runs.localEnvironment)})
					}
					if managedShell {
						return environmentTools.executeManagedShell(live, inv, localEnvironmentSession{Local: environment.Local{Config: cfg}, scope: environment.ResourceHash(*t.runs.localEnvironment)})
					}
					return t.runs.localExecute(live, cfg, inv.Call.Name, inv.Call.Arguments), nil
				}
			}
		}
		if managedFile || managedShell {
			if t.runs.localEnvironment == nil {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
			session := localEnvironmentSession{Local: environment.Local{Config: *t.runs.localEnvironment}, scope: environment.ResourceHash(*t.runs.localEnvironment)}
			if managedFile {
				return environmentTools.executeManagedFile(live, inv, session)
			}
			return environmentTools.executeManagedShell(live, inv, session)
		}
		if t.runs.environmentExecute == nil {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		return t.runs.environmentExecute(live, inv.Call.Name, inv.Call.Arguments), nil
	}
	instance, err := b.computer().Ensure(live)
	if err != nil {
		if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
			return tool.Result{}, err
		}
		return computerFailure(err), nil
	}
	if managedFile || managedShell {
		if managedFile {
			files, ok := t.runs.computerProvider.(guestFileDriver)
			if !ok {
				result = tool.Failure("PROVIDER_UNAVAILABLE")
			} else {
				result, executionErr = environmentTools.executeManagedFile(live, inv, guestFileSession{driver: files, instance: instance})
			}
		} else {
			shell, ok := t.runs.computerProvider.(guestShellDriver)
			if !ok {
				result = tool.Failure("PROVIDER_UNAVAILABLE")
			} else {
				result, executionErr = environmentTools.executeManagedShell(live, inv, guestShellSession{driver: shell, instance: instance})
			}
		}
		if executionErr != nil {
			if t.codeOwner == nil && live.Err() == nil {
				executionErr = errors.Join(executionErr, b.computer().Idle(live))
			}
			return result, executionErr
		}
	} else {
		result = driver.ExecuteEnvironment(live, instance, inv.Call.Name, inv.Call.Arguments)
	}
	if t.codeOwner == nil && live.Err() == nil {
		// Nested calls leave disposal to their cell, after all siblings drain.
		// A direct call checkpoints before delivery. Unprovable mutations remain
		// unknown; recovery never repeats the accepted effect.
		if err := b.computer().Idle(live); err != nil {
			if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
				return tool.Result{}, err
			}
			if inv.Call.Name != "builtin__read" {
				return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
			}
			return computerFailure(err), nil
		}
	}
	return result, nil
}
