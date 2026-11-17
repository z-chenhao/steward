package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	taskcontract "github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type automationTaskLabel struct {
	event, owner, graph, brief, title string
	version, epoch                    int64
	managed                           bool
}

// A title belongs to the new Task admission, not the calendar calculation.
// Until this pure preparation is accepted, changing selection rejects the
// attempt. Task creation and its immutable title provenance commit together.
func (r *Runs) automationTaskLabelFence(ctx context.Context, tx *sql.Tx, p automationTaskLabel) error {
	var owner, ready, graph, brief string
	var eligible bool
	var version, epoch int64
	err := tx.QueryRowContext(ctx, `SELECT a.agent_id,c.setup_status,coalesce(k.active_graph,''),f.instruction,a.schedule_version,coalesce(m.epoch,0),a.enabled=1 AND a.schedule_version=f.schedule_version AND a.task_id IS NULL AND a.intent_privacy_epoch=coalesce(m.epoch,0) AND f.state='pending'
 FROM automation_fire f JOIN automation a USING(automation_id) JOIN agent_config c ON c.singleton=1 AND c.agent_id=a.agent_id JOIN rsi_control k ON k.singleton=1 LEFT JOIN memory_privacy m ON m.agent_id=a.agent_id WHERE f.event_id=?`, p.event).Scan(&owner, &ready, &graph, &brief, &version, &epoch, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ErrConflict
	}
	if err != nil {
		return err
	}
	if ready != "ready" {
		return agent.ErrSetup
	}
	if !eligible || owner != p.owner || graph != p.graph || brief != p.brief || version != p.version || epoch != p.epoch {
		return agent.ErrConflict
	}
	if p.managed {
		return checkScheduleGraphTx(ctx, tx, p.graph)
	}
	return nil
}

func (r *Runs) prepareAutomationTaskLabels(ctx context.Context) (map[string]automationTaskLabel, error) {
	result := map[string]automationTaskLabel{}
	tx, err := r.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	managed, err := managedService(ctx, tx, taskcontract.LabelContract)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.event_id,a.agent_id,coalesce(k.active_graph,''),f.instruction,a.schedule_version,coalesce(m.epoch,0)
 FROM automation_fire f JOIN automation a USING(automation_id) JOIN agent_config c ON c.singleton=1 AND c.agent_id=a.agent_id JOIN rsi_control k ON k.singleton=1 LEFT JOIN memory_privacy m ON m.agent_id=a.agent_id
 WHERE f.state='pending' AND a.enabled=1 AND a.schedule_version=f.schedule_version AND a.task_id IS NULL AND a.intent_privacy_epoch=coalesce(m.epoch,0) ORDER BY f.fire_at,f.event_id`)
	if err != nil {
		return nil, err
	}
	plans := []automationTaskLabel{}
	for rows.Next() {
		p := automationTaskLabel{managed: managed}
		if err = rows.Scan(&p.event, &p.owner, &p.graph, &p.brief, &p.version, &p.epoch); err != nil {
			rows.Close()
			return nil, err
		}
		plans = append(plans, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Rollback(); err != nil {
		return nil, err
	}
	catalog := rsi.NewCatalog(r.host.DataRoot())
	if configured := r.host.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	for _, p := range plans {
		if !p.managed {
			// Explicit pre-task-label activation compatibility; never a failure fallback.
			p.title = task.Title(p.brief)
		} else {
			control := r.host.Plugins()
			frozen := p
			control.Fence = func(ctx context.Context, tx *sql.Tx) error { return r.automationTaskLabelFence(ctx, tx, frozen) }
			host := rsi.Host{Catalog: catalog, Store: control}
			input, _ := json.Marshal(taskcontract.LabelFacts{Text: p.brief, Explicit: false})
			output, err := host.ReadService(ctx, p.graph, taskcontract.LabelContract, input, nil)
			if errors.Is(err, agent.ErrConflict) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var value taskcontract.LabelPlan
			if err = rsiplugin.DecodeResult(output, &value); err != nil || !validTaskLabel(task.ManageRequest{Task: p.brief}, value.Title) {
				return nil, rsi.ErrInvalid
			}
			p.title = value.Title
		}
		result[p.event] = p
	}
	return result, nil
}
