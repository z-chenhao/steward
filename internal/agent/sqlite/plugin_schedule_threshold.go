package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type scheduleThresholdPlan struct {
	event, graph, args         string
	version, at, latest, epoch int64
	observed                   sql.NullInt64
	managed                    bool
	facts                      contract.ThresholdFacts
	decision                   contract.ThresholdResult
}

// Read the accepted invocation and original Root/fire, never caller-supplied IDs.
func (t *tools) readScheduleThreshold(ctx context.Context, tx *sql.Tx, inv tool.Invocation) (*scheduleThresholdPlan, error) {
	if err := t.fence(ctx, tx); err != nil {
		return nil, err
	}
	var name, args string
	if err := tx.QueryRowContext(ctx, `SELECT tool_name,arguments_json FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status='running'`, inv.ID, t.claim.RunID()).Scan(&name, &args); err != nil {
		return nil, err
	}
	if name != automation.ObserveName || !tool.Validate(automation.ObserveSchema, args) {
		return nil, nil
	}
	p := &scheduleThresholdPlan{args: args}
	var value struct {
		Value int64 `json:"value"`
	}
	if err := json.Unmarshal([]byte(args), &value); err != nil {
		return nil, err
	}
	p.facts.Value = value.Value
	err := tx.QueryRowContext(ctx, `SELECT f.event_id,coalesce(b.graph_revision,''),a.version,f.fire_at,a.last_observed_fire,a.intent_privacy_epoch,f.observed_value,f.below_value,a.is_below,
 EXISTS(SELECT 1 FROM json_each(b.managed_services_json) s WHERE s.value=?)
 FROM automation_fire f JOIN automation a USING(automation_id) JOIN run r ON r.run_id=f.root_run_id LEFT JOIN rsi_run b ON b.run_id=r.run_id
 WHERE f.root_run_id=? AND a.enabled=1 AND a.schedule_version=f.schedule_version AND f.below_value IS NOT NULL AND r.worker_position='root'
 AND a.intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=a.agent_id),0)`, contract.Threshold, t.claim.RunID()).Scan(&p.event, &p.graph, &p.version, &p.at, &p.latest, &p.epoch, &p.observed, &p.facts.Below, &p.facts.Entered, &p.managed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.facts.Fresh = p.at > p.latest
	return p, nil
}

func (t *tools) scheduleThresholdFence(ctx context.Context, tx *sql.Tx, inv tool.Invocation, p scheduleThresholdPlan) error {
	current, err := t.readScheduleThreshold(ctx, tx, inv)
	if err != nil {
		return err
	}
	if current == nil || current.observed.Valid || current.event != p.event || current.graph != p.graph || current.args != p.args || current.version != p.version || current.at != p.at || current.latest != p.latest || current.epoch != p.epoch || current.managed != p.managed || current.facts != p.facts {
		return agent.ErrConflict
	}
	return checkScheduleGraphTx(ctx, tx, p.graph)
}

func (t *tools) prepareScheduleThreshold(ctx context.Context, inv tool.Invocation) (*scheduleThresholdPlan, error) {
	tx, err := t.runs.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := t.readScheduleThreshold(ctx, tx, inv)
	// Already accepted observations replay without consulting the producer.
	// A Run whose immutable snapshot predates this service retains its interpreter.
	if err != nil || p == nil || p.observed.Valid || !p.managed {
		return nil, err
	}
	if err = tx.Rollback(); err != nil {
		return nil, err
	}
	catalog := rsi.NewCatalog(t.runs.host.DataRoot())
	if configured := t.runs.host.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	control := t.runs.host.Plugins()
	control.Fence = func(ctx context.Context, tx *sql.Tx) error { return t.scheduleThresholdFence(ctx, tx, inv, *p) }
	host := rsi.Host{Catalog: catalog, Store: control}
	input, _ := json.Marshal(p.facts)
	output, err := host.ReadService(ctx, p.graph, contract.Threshold, input, nil)
	if err != nil {
		return nil, err
	}
	if err = rsiplugin.DecodeResult(output, &p.decision); err != nil {
		return nil, err
	}
	// A policy cannot turn late evidence into a new causal state or notification.
	if !p.facts.Fresh && (p.decision.Notify || p.decision.IsBelow != p.facts.Entered) {
		return nil, rsi.ErrInvalid
	}
	return p, nil
}
