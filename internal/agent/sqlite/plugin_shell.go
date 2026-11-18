package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/plugin/shellpolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type shellSession interface {
	ResourceHash() string
	ExecuteShell(context.Context, environment.ShellCommand) tool.Result
}

type guestShellDriver interface {
	EnvironmentResourceHash() string
	ExecuteEnvironmentShell(context.Context, computer.Instance, environment.ShellCommand) tool.Result
}
type guestShellSession struct {
	driver   guestShellDriver
	instance computer.Instance
}

func (s guestShellSession) ResourceHash() string { return s.driver.EnvironmentResourceHash() }
func (s guestShellSession) ExecuteShell(ctx context.Context, c environment.ShellCommand) tool.Result {
	return s.driver.ExecuteEnvironmentShell(ctx, s.instance, c)
}

func validShellLimits(l contract.Limits, requested *int) bool {
	return l.TimeoutSeconds >= 1 && l.TimeoutSeconds <= 120 && l.OutputBytes >= 1 && l.OutputBytes <= 48000 && (requested == nil || l.TimeoutSeconds <= *requested)
}

// Only the original command crosses the Host-to-driver boundary. The pure
// recipe receives numeric ceilings, never command, paths or approval authority.
func (t *tools) executeManagedShell(ctx context.Context, inv tool.Invocation, driver shellSession) (tool.Result, error) {
	var args struct {
		Command string      `json:"command"`
		Timeout json.Number `json:"timeout"`
	}
	var resource, requestHash string
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		role, name, raw, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if role != "worker" || name != "builtin__bash" {
			return tool.ErrApprovalDenied
		}
		if err = json.Unmarshal([]byte(raw), &args); err != nil {
			return err
		}
		inv.Call.Name, inv.Call.Arguments = name, raw
		requestHash = digest(name + ":" + raw)
		return tx.QueryRowContext(ctx, `SELECT json_extract(j.value,'$.resource_hash') FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE r.run_id=? AND json_extract(j.value,'$.name')=?`, t.claim.RunID(), name).Scan(&resource)
	})
	if err != nil {
		return environmentMethodFailure(err)
	}
	if resource == "" || driver.ResourceHash() != resource {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	var requested *int
	if args.Timeout != "" {
		value, err := strconv.ParseFloat(string(args.Timeout), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 1 || value > 120 || math.Trunc(value) != value {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		seconds := int(value)
		requested = &seconds
	}
	if t.runs.plugins == nil {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return environmentMethodFailure(err)
	}
	if revision == "" {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	if _, err = h.ServiceInstance(ctx, revision, contract.Contract); err != nil {
		return environmentMethodFailure(err)
	}
	if err = h.Store.CheckGraph(ctx, revision); err != nil {
		return environmentMethodFailure(err)
	}
	identity, err := environment.ShellDriverFingerprint()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	identity = digest(resource + ":" + identity)
	var limits contract.Limits
	var savedGraph, savedDriver, savedRequest string
	cached := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT graph_revision,driver_hash,request_hash,timeout_seconds,output_bytes FROM shell_method_plan WHERE invocation_id=?`, inv.ID).Scan(&savedGraph, &savedDriver, &savedRequest, &limits.TimeoutSeconds, &limits.OutputBytes)
		if err == sql.ErrNoRows {
			return nil
		}
		cached = err == nil
		return err
	})
	if err != nil {
		return tool.Result{}, err
	}
	if cached {
		if savedGraph != revision || savedDriver != identity || savedRequest != requestHash || !validShellLimits(limits, requested) {
			return environmentMethodFailure(tool.ErrReplay)
		}
	} else {
		input, _ := json.Marshal(contract.Facts{Ceilings: contract.Ceilings{TimeoutSeconds: 120, OutputBytes: 48000}, RequestedTimeout: requested})
		raw, err := h.ReadService(ctx, revision, contract.Contract, input, nil)
		if err != nil {
			return environmentMethodFailure(err)
		}
		if rsiplugin.DecodeResult(raw, &limits) != nil || !validShellLimits(limits, requested) {
			return environmentMethodFailure(rsi.ErrInvalid)
		}
	}
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		if cached {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO shell_method_plan VALUES(?,?,?,?,?,?)`, inv.ID, revision, identity, requestHash, limits.TimeoutSeconds, limits.OutputBytes)
		return err
	})
	if err != nil {
		return environmentMethodFailure(err)
	}
	result := driver.ExecuteShell(ctx, environment.ShellCommand{Command: args.Command, Limits: environment.ShellLimits{TimeoutSeconds: limits.TimeoutSeconds, OutputBytes: limits.OutputBytes}})
	if result.Error != nil {
		return result, nil
	}
	var value struct {
		Command   *string `json:"command"`
		Output    *string `json:"output"`
		Exit      *int    `json:"exit_code"`
		TimedOut  *bool   `json:"timed_out"`
		Truncated *bool   `json:"truncated"`
	}
	if json.Unmarshal([]byte(result.Text), &value) != nil || value.Command == nil || *value.Command != args.Command || value.Output == nil || !utf8.ValidString(*value.Output) || len(*value.Output) > 3*limits.OutputBytes || value.Exit == nil || value.TimedOut == nil || value.Truncated == nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	return result, nil
}
