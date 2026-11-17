package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func readScheduleDefaults(ctx context.Context, host rsi.Host, graph string) (automation.EditorDefaults, error) {
	raw, err := host.ReadService(ctx, graph, contract.Defaults, json.RawMessage(`{}`), nil)
	if err != nil {
		return automation.EditorDefaults{}, err
	}
	var value contract.DefaultsResult
	if err = rsiplugin.DecodeResult(raw, &value); err != nil {
		return automation.EditorDefaults{}, err
	}
	result := automation.EditorDefaults{Frequency: value.Frequency, Date: value.Date, Clock: value.Clock, Timezone: value.Timezone, Cron: value.Cron, Weekday: value.Weekday, Day: value.Day, Enabled: value.Enabled}
	if err = result.Validate(); err != nil {
		return result, rsi.ErrInvalid
	}
	return result, nil
}

// Public draft metadata has no user text or identifiers. Existing records use
// their accepted recipe instead, so opening them never starts a stopped method.
func (r *Runs) OwnerScheduleDefaults(ctx context.Context) (automation.EditorDefaults, error) {
	tx, err := r.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return automation.EditorDefaults{}, err
	}
	defer tx.Rollback()
	managed, err := managedService(ctx, tx, contract.Defaults)
	if err != nil {
		return automation.EditorDefaults{}, err
	}
	if !managed {
		return automation.LegacyEditorDefaults(), nil
	}
	var graph string
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&graph); err != nil {
		return automation.EditorDefaults{}, err
	}
	if err = tx.Rollback(); err != nil {
		return automation.EditorDefaults{}, err
	}
	catalog := rsi.NewCatalog(r.host.DataRoot())
	if configured := r.host.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	control := r.host.Plugins()
	control.Fence = func(ctx context.Context, tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&current); err != nil {
			return err
		}
		if current != graph {
			return agent.ErrConflict
		}
		return checkScheduleGraphTx(ctx, tx, graph)
	}
	return readScheduleDefaults(ctx, rsi.Host{Catalog: catalog, Store: control}, graph)
}
