package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (t *tools) appsInvocationContext(ctx context.Context, inv tool.Invocation) (context.Context, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Method)
	if err != nil || !managed {
		return ctx, err
	}
	if t.runs.plugins == nil {
		return connectedapps.WithMethod(ctx, nil), nil
	}
	host := t.runs.pluginHostWithFence(t.fence)
	return connectedapps.WithMethod(ctx, func(ctx context.Context, name, arguments string, request contract.Request, source func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error) {
		check := t.connectorInvocationFence(inv, name, arguments, t.checkAppsResource)
		if err := check(ctx); err != nil {
			return contract.Plan{}, err
		}
		revision, err := host.Store.Binding(ctx, t.claim.RunID())
		if err != nil || revision == "" {
			if err == nil {
				err = rsi.ErrNotFound
			}
			return contract.Plan{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.Plan{}, err
		}
		port := rsi.Resource{
			Contract:     contract.Source,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"phase":{"type":"string","enum":["binding","account","descriptor","search","execute"]},"limit":{"type":"integer","minimum":0,"maximum":1000}},"required":["phase","limit"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"needs_account":{"type":"boolean"},"has_cursor":{"type":"boolean"},"bytes":{"type":"integer","minimum":0}},"required":["needs_account","has_cursor","bytes"],"additionalProperties":false}`),
			Invoke: func(ctx context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
				var request contract.SourceRequest
				if err := rsiplugin.DecodeResource(raw, &request); err != nil {
					return nil, err
				}
				if err := check(ctx); err != nil {
					return nil, err
				}
				facts, err := source(ctx, key, request)
				t.runs.host.boundary("after_apps_source")
				if err != nil {
					return nil, err
				}
				return json.Marshal(facts)
			},
		}
		raw, err := host.DriveService(ctx, revision, contract.Method, input, nil, check, []rsi.Resource{port})
		t.runs.host.boundary("after_apps_method")
		if authority := check(ctx); authority != nil {
			return contract.Plan{}, authority
		}
		if authority := host.Store.CheckGraph(ctx, revision); authority != nil {
			return contract.Plan{}, authority
		}
		var plan contract.Plan
		if err == nil {
			err = rsiplugin.DecodeResult(raw, &plan)
		}
		return plan, err
	}), nil
}

// This is an original accepted account resource, not an indefinite grant after
// owner disconnect/config replacement. Load authoritative state in the same
// transaction; a stale in-memory Registry alone cannot fence another process.
func (t *tools) checkAppsResource(ctx context.Context, tx *sql.Tx, invocation string) error {
	var name, resource, provider string
	err := tx.QueryRowContext(ctx, `SELECT i.tool_name,coalesce(json_extract(j.value,'$.resource_hash'),''),coalesce(json_extract(j.value,'$.provider'),'')
FROM tool_invocation i JOIN run r USING(run_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j
WHERE i.invocation_id=? AND i.run_id=? AND i.status='running' AND json_extract(j.value,'$.name')=i.tool_name`, invocation, t.claim.RunID()).Scan(&name, &resource, &provider)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(name, contract.ToolPrefix) || resource == "" || provider != "composio" {
		return rsi.ErrRevoked
	}
	current, exists := t.runs.registry.Resolve(name)
	if !exists || current.Spec.ResourceHash != resource || current.Spec.Provider != provider {
		return rsi.ErrRevoked
	}
	state, err := readAppsStateTx(ctx, tx)
	if err != nil {
		return err
	}
	if _, valid := appsConnectionForSpec(state, tool.Spec{Name: name, Provider: provider, ResourceHash: resource}); valid {
		return nil
	}
	return rsi.ErrRevoked
}

func readAppsStateTx(ctx context.Context, tx *sql.Tx) (connectedapps.State, error) {
	var state connectedapps.State
	var document string
	if err := tx.QueryRowContext(ctx, `SELECT document_json FROM connected_apps WHERE singleton=1`).Scan(&document); err != nil {
		return state, err
	}
	if err := json.Unmarshal([]byte(document), &state); err != nil {
		return state, err
	}
	if state.KeyRef == "" {
		// Existing explicit environment-default bindings freeze this reference.
		// Resolving credentials inside this SQL transaction is unnecessary; the
		// fixed source client resolves the actual secret only at original IO.
		state.KeyRef = connectedapps.DefaultKeyRef
	}
	return state, nil
}

func appsConnectionForSpec(state connectedapps.State, spec tool.Spec) (connectedapps.Connection, bool) {
	if spec.Provider != "composio" || spec.ResourceHash == "" {
		return connectedapps.Connection{}, false
	}
	for _, connection := range state.Connections {
		prefix := contract.ToolPrefix + strings.ReplaceAll(connection.ID, "-", "") + "__"
		if spec.Name != prefix+"search_tools" && spec.Name != prefix+"execute" {
			continue
		}
		if connection.Status == "active" && connectedapps.BindingHash(state, connection) == spec.ResourceHash {
			return connection, true
		}
		return connectedapps.Connection{}, false
	}
	return connectedapps.Connection{}, false
}
