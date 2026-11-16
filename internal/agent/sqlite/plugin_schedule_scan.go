package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/event"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type dueAutomation struct {
	id, owner, kind, spec, zone, instruction string
	at, version                              int64
	below                                    sql.NullInt64
	graph                                    sql.NullString
	eligible, stale                          bool
	next                                     time.Time
}

func (r *Runs) readDueAutomations(ctx context.Context, now time.Time) ([]dueAutomation, error) {
	rows, err := r.host.db.QueryContext(ctx, `SELECT a.automation_id,a.agent_id,a.trigger_type,a.trigger_spec,a.timezone,a.instruction,a.next_fire_at,a.schedule_version,a.below_value,a.method_graph,
 a.intent_privacy_epoch=coalesce(p.epoch,0) AND EXISTS(SELECT 1 FROM agent_config c WHERE c.singleton=1 AND c.agent_id=a.agent_id AND c.setup_status='ready')
 FROM automation a LEFT JOIN memory_privacy p USING(agent_id) WHERE a.enabled=1 AND a.next_fire_at<=? ORDER BY a.next_fire_at,a.automation_id LIMIT 100`, now.UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []dueAutomation
	for rows.Next() {
		var d dueAutomation
		if err = rows.Scan(&d.id, &d.owner, &d.kind, &d.spec, &d.zone, &d.instruction, &d.at, &d.version, &d.below, &d.graph, &d.eligible); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// Automatic advancement must keep the original schedule identity and policy.
// A concurrent edit, second scanner or privacy barrier invalidates this input;
// it never authorizes a replacement selection or a recomputed native result.
func dueAutomationFence(ctx context.Context, tx *sql.Tx, d dueAutomation) error {
	var at sql.NullInt64
	var graph sql.NullString
	var version int64
	var enabled, eligible bool
	err := tx.QueryRowContext(ctx, `SELECT a.next_fire_at,a.schedule_version,a.enabled,a.method_graph,
 a.intent_privacy_epoch=coalesce(p.epoch,0) AND EXISTS(SELECT 1 FROM agent_config c WHERE c.singleton=1 AND c.agent_id=a.agent_id AND c.setup_status='ready')
 FROM automation a LEFT JOIN memory_privacy p USING(agent_id) WHERE a.automation_id=? AND a.agent_id=?`, d.id, d.owner).Scan(&at, &version, &enabled, &graph, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ErrConflict
	}
	if err != nil {
		return err
	}
	if !enabled || !at.Valid || at.Int64 != d.at || version != d.version || graph != d.graph || d.graph.Valid && !eligible {
		return agent.ErrConflict
	}
	return nil
}

func (r *Runs) prepareDueAutomation(ctx context.Context, d *dueAutomation, now time.Time) error {
	if !d.graph.Valid {
		if d.kind == "cron" {
			at, err := automation.HistoricalNext(d.kind, d.spec, d.zone, now)
			d.next = at
			return err
		}
		return nil
	}
	if !d.eligible {
		d.stale = true
		return nil
	}
	catalog := rsi.NewCatalog(r.host.DataRoot())
	if configured := r.host.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	control := r.host.Plugins()
	frozen := *d
	control.Fence = func(ctx context.Context, tx *sql.Tx) error { return dueAutomationFence(ctx, tx, frozen) }
	host := rsi.Host{Catalog: catalog, Store: control}
	if d.kind != "cron" {
		return host.Store.CheckGraph(ctx, d.graph.String)
	}
	input, _ := json.Marshal(contract.NextFacts{Type: d.kind, Spec: d.spec, Timezone: d.zone, After: now})
	output, err := host.ReadService(ctx, d.graph.String, contract.Next, input, nil)
	if err != nil {
		return err
	}
	var value contract.NextResult
	if err = rsiplugin.DecodeResult(output, &value); err != nil {
		return rsi.ErrInvalid
	}
	if value.ErrorCode == "INVALID_ARGUMENT" && value.Next == "" {
		return automation.ErrInvalid
	}
	if value.ErrorCode != "" {
		return rsi.ErrInvalid
	}
	d.next, err = time.Parse(time.RFC3339Nano, value.Next)
	if err != nil || !d.next.After(now) || d.next.UTC().Format(time.RFC3339Nano) != value.Next {
		return rsi.ErrInvalid
	}
	return nil
}

// ScheduleAutomations computes pure calendar policy outside the writer, then
// accepts the original due fire + next instant + Outbox in one transaction.
// Missed occurrences still coalesce to one original fire; no Task or Run is made.
func (r *Runs) ScheduleAutomations(ctx context.Context, now time.Time) error {
	ds, err := r.readDueAutomations(ctx, now)
	if err != nil {
		return agent.ErrStorage
	}
	if len(ds) == 0 {
		return nil
	}
	for i := range ds {
		if err = r.prepareDueAutomation(ctx, &ds[i], now); err != nil {
			if errors.Is(err, agent.ErrConflict) {
				ds[i].stale = true
				continue
			}
			return err
		}
	}
	r.host.boundary("before_automation_fire")
	err = r.write(ctx, func(tx *sql.Tx) error {
		for _, d := range ds {
			if d.stale {
				continue
			}
			if err := dueAutomationFence(ctx, tx, d); err != nil {
				if errors.Is(err, agent.ErrConflict) {
					continue
				}
				return err
			}
			if d.graph.Valid {
				if err := checkScheduleGraphTx(ctx, tx, d.graph.String); err != nil {
					return err
				}
			}
			ev, e := event.New("/steward/automations", "ai.steward.automation_fired.v1", d.id, struct {
				AutomationID string `json:"automation_id"`
				Version      int64  `json:"schedule_version"`
				FireAt       int64  `json:"fire_at"`
			}{d.id, d.version, d.at})
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO automation_fire(event_id,automation_id,schedule_version,fire_at,instruction,below_value,state,method_graph) VALUES(?,?,?,?,?,?,'pending',?)`, ev.ID(), d.id, d.version, d.at, d.instruction, d.below, d.graph); e != nil {
				return e
			}
			if e = eventsqlite.Enqueue(ctx, tx, ev); e != nil {
				return e
			}
			var next any
			if d.kind == "cron" {
				next = d.next.UnixMicro()
			}
			if _, e = tx.ExecContext(ctx, `UPDATE automation SET next_fire_at=?,updated_at=?,version=version+1 WHERE automation_id=?`, next, now.UnixMicro(), d.id); e != nil {
				return e
			}
		}
		r.host.boundary("before_automation_fire_commit")
		return nil
	})
	if err == nil {
		r.host.boundary("after_automation_fire_commit")
	}
	return err
}
