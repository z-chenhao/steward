package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	appsmethods "github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/mcp"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// InvocationContext binds an external adapter to this accepted Run/CodeMode
// owner. The invocation ID is correlation only; each source release verifies
// the actual claim, persisted arguments, privacy epoch and original graph.
func (t *tools) InvocationContext(ctx context.Context, inv tool.Invocation) (context.Context, error) {
	if strings.HasPrefix(inv.Call.Name, appsmethods.ToolPrefix) {
		return t.appsInvocationContext(ctx, inv)
	}
	if !strings.HasPrefix(inv.Call.Name, "mcp__") {
		return ctx, nil
	}
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Method)
	if err != nil || !managed {
		return ctx, err
	}
	if t.runs.plugins == nil {
		return mcp.WithMethod(ctx, nil), nil
	}
	h := *t.runs.plugins
	store := t.runs.host.Plugins()
	store.Fence = t.fence
	h.Store = store
	return mcp.WithMethod(ctx, func(ctx context.Context, name, args string, source func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error) {
		check := func(ctx context.Context) error {
			return t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
				_, acceptedName, acceptedArgs, err := t.acceptedBuiltin(ctx, tx, inv)
				if err != nil {
					return err
				}
				if acceptedName != name || acceptedArgs != args {
					return tool.ErrApprovalDenied
				}
				if err := t.checkMCPResource(ctx, tx, inv.ID); err != nil {
					return err
				}
				return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
			})
		}
		if err := check(ctx); err != nil {
			return contract.Plan{}, err
		}
		revision, err := store.Binding(ctx, t.claim.RunID())
		if err != nil || revision == "" {
			if err == nil {
				err = rsi.ErrNotFound
			}
			return contract.Plan{}, err
		}
		port := rsi.Resource{Contract: contract.Source,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"phase":{"type":"string","enum":["list","call"]},"cursor":{"type":"string","maxLength":1048576}},"required":["phase","cursor"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"next_cursor":{"type":"string","maxLength":1048576},"text":{"type":"array","items":{"type":"string","maxLength":1048576},"maxItems":65536},"is_error":{"type":"boolean"}},"required":["next_cursor","text","is_error"],"additionalProperties":false}`),
			Invoke: func(ctx context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
				var input contract.SourceRequest
				if err := rsiplugin.DecodeResource(raw, &input); err != nil {
					return nil, err
				}
				if err := check(ctx); err != nil {
					return nil, err
				}
				reply, err := source(ctx, key, input)
				t.runs.host.boundary("after_mcp_source")
				if err != nil {
					return nil, err
				}
				return json.Marshal(reply)
			}}
		raw, err := h.DriveService(ctx, revision, contract.Method, json.RawMessage(`{}`), nil, check, []rsi.Resource{port})
		t.runs.host.boundary("after_mcp_method")
		if authority := check(ctx); authority != nil {
			return contract.Plan{}, authority
		}
		if authority := h.Store.CheckGraph(ctx, revision); authority != nil {
			return contract.Plan{}, authority
		}
		var plan contract.Plan
		if err == nil {
			err = rsiplugin.DecodeResult(raw, &plan)
		}
		return plan, err
	}), nil
}

// The frozen tool grants a particular configured peer, not indefinite access
// after its configuration is removed/replaced. Namespace presentation remains
// independent: its source/description is deliberately absent from this hash.
func (t *tools) checkMCPResource(ctx context.Context, tx *sql.Tx, id string) error {
	var name, resource string
	err := tx.QueryRowContext(ctx, `SELECT i.tool_name,coalesce(json_extract(j.value,'$.resource_hash'),'')
FROM tool_invocation i JOIN run r USING(run_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j
WHERE i.invocation_id=? AND i.run_id=? AND i.status='running' AND json_extract(j.value,'$.name')=i.tool_name`, id, t.claim.RunID()).Scan(&name, &resource)
	if err != nil {
		return err
	}
	current, exists := t.runs.registry.Resolve(name)
	if !strings.HasPrefix(name, "mcp__") || resource == "" || !exists || current.Spec.ResourceHash != resource {
		return rsi.ErrRevoked
	}
	return nil
}

func (t *tools) commitMCPResult(ctx context.Context, tx *sql.Tx, id string, result tool.Result) (tool.Result, error) {
	err := t.checkMCPResource(ctx, tx, id)
	if err == nil {
		err = t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
	}
	if err == nil {
		result.HostFence = nil
		return result, nil
	}
	if !errors.Is(err, rsi.ErrRevoked) {
		return tool.Result{}, err
	}
	// A still-authorized execution owner must record uncertainty when its
	// already-dispatched effect loses source/graph authority before publication.
	// This is the original Tool receipt, not a retry or another effect journal.
	_, policy, err := t.load(ctx, tx, id)
	if err != nil {
		return tool.Result{}, err
	}
	if policy.Replay == "never" {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	return tool.Failure("PROVIDER_UNAVAILABLE"), nil
}
