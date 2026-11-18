package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type computerParameterRequest struct {
	invocation              tool.Invocation
	call                    computer.BridgeCall
	args                    []any
	hash, surface, revision string
	service                 string
}

// Preparation reads only the accepted request and its private surface kind.
// It never acquires a Computer or exposes handles, Native data or live content.
// A completed plan is committed before the existing approval is created.
func (t *tools) prepareComputerParameters(ctx context.Context, id string) (tool.Result, error) {
	if t.codeOwner == nil {
		return tool.Result{}, nil
	}
	var request computerParameterRequest
	applies, managed := false, false
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		inv, _, err := t.load(ctx, tx, id)
		if err != nil {
			return err
		}
		// Recovery of an already started physical action uses its original Tool
		// ledger, even when a method is stopped. Never plan again to resend it.
		if inv.Status != "queued" && inv.Status != "waiting" {
			return nil
		}
		if json.Unmarshal([]byte(inv.Call.Arguments), &request.call) != nil || !computer.ActionParameterMethod(inv.Call.Name, request.call.Method) {
			return nil
		}
		definition, ok := t.catalog().Resolve(inv.Call.Name)
		if !ok || !tool.Validate(definition.Spec.InputSchema, inv.Call.Arguments) {
			return nil
		}
		request.invocation = inv
		if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM tool_invocation WHERE invocation_id=? AND run_id=? AND parent_invocation_id=?`, id, t.claim.RunID(), t.codeOwner.req.ExecutionID).Scan(&request.hash); err != nil {
			return err
		}
		request.service, managed, err = computerParameterService(ctx, tx, t.claim.RunID(), request.call.Method)
		if err != nil {
			return err
		}
		if !managed {
			return nil
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(graph_revision,'') FROM rsi_run WHERE run_id=?`, t.claim.RunID()).Scan(&request.revision); err != nil {
			return err
		}
		if inv.Call.Name == "builtin__computer_invoke" {
			request.surface = "native"
			var raw string
			err := tx.QueryRowContext(ctx, `SELECT state_json FROM computer_use WHERE execution_id=?`, t.codeOwner.req.ExecutionID).Scan(&raw)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			var state computer.UseState
			if raw != "" && json.Unmarshal([]byte(raw), &state) != nil {
				return agent.ErrStorage
			}
			if h := state.Handles[request.call.Token]; h != nil && h.Target.Kind == "tab" {
				request.surface = "tab"
			}
		}
		applies = true
		return nil
	})
	if err != nil || !applies || !managed {
		return tool.Result{}, err
	}
	if len(request.call.Args) > 0 && string(request.call.Args) != "null" {
		if json.Unmarshal(request.call.Args, &request.args) != nil {
			var value any
			if json.Unmarshal(request.call.Args, &value) != nil {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			request.args = []any{value}
		}
	}
	cached, err := t.loadComputerParameters(ctx, request)
	if err != nil {
		return environmentMethodFailure(err)
	}
	if cached != nil && request.service != contract.BrowserUploadContract {
		return tool.Result{}, nil
	}
	if t.runs.plugins == nil || request.revision == "" {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	host := t.runs.pluginHostWithFence(t.computerMethodFence)
	if request.service == contract.BrowserUploadContract {
		// This request is still queued/waiting and was fenced above. The running
		// builtin admission check remains exclusive to actual execution.
		if _, err := t.bindComputerBrowserUploader(ctx, host, request.revision); err != nil {
			return environmentMethodFailure(err)
		}
		if cached != nil {
			return tool.Result{}, nil
		}
	}
	facts, _ := json.Marshal(contract.ActionFacts{Method: request.call.Method, Surface: request.surface})
	data, _ := json.Marshal(contract.ActionArguments{Args: computerParameterArguments(request.call.Method, request.surface, request.args)})
	raw, err := host.ReadService(ctx, request.revision, request.service, facts, []rsi.InputData{{ID: "arguments", Data: data}})
	if err != nil {
		return environmentMethodFailure(err)
	}
	var parameters computer.ActionParameters
	if rsiplugin.DecodeResult(raw, &parameters) != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	parameters = computer.ResolveTransferPresentation(request.call.Method, request.args, parameters)
	if request.service == contract.BrowserUploadContract && parameters.Failure == "" && !computer.HasUploadPresentation(parameters) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if !computer.ValidActionParameters(request.call.Method, request.surface, request.args, parameters) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	canonical, err := encoded(parameters)
	if err != nil {
		return tool.Result{}, err
	}
	t.runs.host.boundary("before_computer_parameters_commit")
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.computerMethodFence(ctx, tx); err != nil {
			return err
		}
		var original string
		if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status IN ('queued','waiting') AND attempt_count=0`, id, t.claim.RunID()).Scan(&original); err != nil {
			return err
		}
		if original != request.hash {
			return tool.ErrReplay
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer_action_plan WHERE invocation_id=?)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		} // a concurrent completed plan wins; never overwrite it
		if _, err := tx.ExecContext(ctx, `INSERT INTO computer_action_plan VALUES(?,?,?,?,?,?,?)`, id, request.revision, request.hash, request.call.Method, request.surface, digest(canonical), canonical); err != nil {
			return err
		}
		return runEvent(ctx, tx, t.claim.RunID(), "computer.action_parameters.frozen", 1)
	})
	if err != nil {
		return environmentMethodFailure(err)
	}
	_, err = t.loadComputerParameters(ctx, request)
	if err != nil {
		return environmentMethodFailure(err)
	}
	return tool.Result{}, nil
}

func (t *tools) loadComputerParameters(ctx context.Context, request computerParameterRequest) (*computer.ActionParameters, error) {
	var out *computer.ActionParameters
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.computerMethodFence(ctx, tx); err != nil {
			return err
		}
		var revision, hash, method, surface, planHash string
		var raw sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT graph_revision,request_hash,method,surface_kind,plan_hash,plan_json FROM computer_action_plan WHERE invocation_id=?`, request.invocation.ID).Scan(&revision, &hash, &method, &surface, &planHash, &raw)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if !raw.Valid {
			return agent.ErrClaimLost
		}
		if revision != request.revision || hash != request.hash || method != request.call.Method || surface != request.surface || digest(raw.String) != planHash {
			return tool.ErrReplay
		}
		var parameters computer.ActionParameters
		if json.Unmarshal([]byte(raw.String), &parameters) != nil || !computer.ValidActionParameters(method, surface, request.args, parameters) {
			return rsi.ErrInvalid
		}
		out = &parameters
		return nil
	})
	return out, err
}

func (t *tools) computerActionParameterReader(ctx context.Context, inv tool.Invocation) (computer.ActionParameterReader, error) {
	var call computer.BridgeCall
	if err := json.Unmarshal([]byte(inv.Call.Arguments), &call); err != nil {
		return nil, tool.ErrReplay
	}
	var service string
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		service, _, err = computerParameterService(ctx, tx, t.claim.RunID(), call.Method)
		return err
	})
	if err != nil {
		return nil, err
	}
	host, revision, err := t.computerMethodHost(ctx, inv, service)
	if err != nil || host == nil {
		return nil, err
	}
	// The preflight checks the method is still available; execution only reads
	// the already approved domain plan. No plugin runs after approval here.
	return func(ctx context.Context, method, surface string) (computer.ActionParameters, error) {
		var request computerParameterRequest
		request.invocation, request.revision, request.surface = inv, revision, surface
		request.call.Method = method
		err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
			if err := t.computerMethodFence(ctx, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctx, `SELECT request_hash FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status='running'`, inv.ID, t.claim.RunID()).Scan(&request.hash)
		})
		if err != nil {
			return computer.ActionParameters{}, err
		}
		// Re-decode the original accepted args for the independent intent proof.
		var call computer.BridgeCall
		if json.Unmarshal([]byte(inv.Call.Arguments), &call) != nil || call.Method != method {
			return computer.ActionParameters{}, tool.ErrReplay
		}
		if len(call.Args) > 0 && string(call.Args) != "null" {
			if json.Unmarshal(call.Args, &request.args) != nil {
				var value any
				if json.Unmarshal(call.Args, &value) != nil {
					return computer.ActionParameters{}, tool.ErrReplay
				}
				request.args = []any{value}
			}
		}
		p, err := t.loadComputerParameters(ctx, request)
		if err != nil {
			return computer.ActionParameters{}, err
		}
		if p == nil {
			return computer.ActionParameters{}, rsi.ErrNotFound
		}
		return *p, nil
	}, nil
}

// Resource references are irrelevant to argument normalization. Preserve their
// original slots but release no handle/revision/element/coordinate to the method.
func computerParameterArguments(method, surface string, args []any) []any {
	if method == "exportArtifact" {
		return computer.TransferPresentationArguments(args)
	}
	if computer.PlaywrightParameterMethod(method) {
		return computer.PlaywrightParameterArguments(method, args)
	}
	out := append([]any(nil), args...)
	hide := func(index int) {
		if index < len(out) {
			out[index] = nil
		}
	}
	switch method {
	case "click", "scroll", "setValue", "selectText", "performSecondaryAction":
		hide(0)
	case "drag":
		hide(0)
		hide(1)
	case "typeText", "paste", "pressKey":
		if surface == "tab" {
			hide(0)
		}
	}
	return out
}

func computerParameterContract(method string) string {
	if method == "exportArtifact" {
		return contract.TransferPresentationContract
	}
	if computer.PlaywrightParameterMethod(method) {
		return contract.PlaywrightParametersContract
	}
	return contract.ActionParametersContract
}
