package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// managedRunService reads the immutable per-Run migration boundary. Current
// activation never changes an existing execution or its descendants.
func (r *Runs) managedRunService(ctx context.Context, runID, contract string) (bool, error) {
	var managed bool
	err := r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?)`, runID, contract).Scan(&managed)
	return managed, err
}

// featureService executes pure feature policy with the Run's exact frozen graph.
// There is no native fallback after a feature's migration boundary.
func (r *Runs) featureService(ctx context.Context, c agent.RunClaim, contract, operation string, data []byte) ([]byte, error) {
	if r.plugins == nil {
		return nil, rsi.ErrNotFound
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, c.RunID())
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return nil, rsi.ErrNotFound
	}
	b, err := h.ServiceInstance(ctx, revision, contract)
	if err != nil {
		return nil, err
	}
	out, err := h.CallWithInput(ctx, rsiplugin.Execution{RunID: c.RunID()}, b.InstanceID, contract, operation, []byte(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: data}})
	if errors.Is(err, sql.ErrNoRows) {
		err = rsi.ErrNotFound
	}
	return out, err
}
