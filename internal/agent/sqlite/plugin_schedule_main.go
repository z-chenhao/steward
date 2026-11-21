package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Preparation reads accepted arguments under the original claim. The final
// transaction repeats the claim and receipt checks before accepting any effect.
func (t *tools) prepareMainSchedule(ctx context.Context, inv tool.Invocation, now time.Time) (*ownerSchedulePlan, error) {
	tx, err := t.runs.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = t.fence(ctx, tx); err != nil {
		return nil, err
	}
	var name, args, kind string
	if err = tx.QueryRowContext(ctx, `SELECT i.tool_name,i.arguments_json,r.kind FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=? AND i.run_id=? AND i.status='running'`, inv.ID, t.claim.RunID()).Scan(&name, &args, &kind); err != nil {
		return nil, err
	}
	if name != automation.ManageName || kind != "main" {
		return nil, nil
	}
	q, err := automation.DecodeRequest(args)
	if err != nil || (q.Operation != "create" && q.Operation != "update") {
		return nil, nil
	}
	var managed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.schedule-next/1')`, t.claim.RunID()).Scan(&managed); err != nil {
		return nil, err
	}
	if !managed {
		return nil, nil
	} // Explicit historical Run boundary.
	plan := ownerSchedulePlan{ref: q.Ref, version: q.Version, managed: true, requireActive: true, claimFence: t.fence}
	if err = tx.QueryRowContext(ctx, `SELECT a.agent_id,coalesce(p.epoch,0),coalesce(r.graph_revision,'') FROM agent_config a LEFT JOIN memory_privacy p USING(agent_id) JOIN rsi_run r ON r.run_id=? WHERE a.singleton=1`, t.claim.RunID()).Scan(&plan.agent, &plan.epoch, &plan.graph); err != nil {
		return nil, err
	}
	var source string
	if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT message_id FROM main_input WHERE run_id=r.run_id AND sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY sequence DESC LIMIT 1),'') FROM run r WHERE r.run_id=?`, t.claim.RunID()).Scan(&source); err != nil {
		return nil, err
	}
	if source == "" {
		source = inv.ID
	}
	canonical, _ := encoded(q)
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT request_hash FROM automation_request WHERE agent_id=? AND request_key=?`, plan.agent, source+":"+digest(canonical)).Scan(&prior)
	if err == nil {
		if prior != digest(canonical) {
			return nil, agent.ErrConflict
		}
		return nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.schedule-editor/1')`, t.claim.RunID()).Scan(&plan.project); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.schedule-display/1')`, t.claim.RunID()).Scan(&plan.display); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.schedule-instruction-label/1')`, t.claim.RunID()).Scan(&plan.label); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.schedule-defaults/1')`, t.claim.RunID()).Scan(&plan.defaults); err != nil {
		return nil, err
	}
	if err = t.runs.ownerScheduleFence(ctx, tx, plan); err != nil {
		return nil, err
	}
	if err = tx.Rollback(); err != nil {
		return nil, err
	}
	prepared, err := t.runs.computeSchedule(ctx, plan, automation.OwnerConfiguration{Instruction: q.Instruction, Type: q.Type, Spec: q.Spec, Timezone: q.Timezone, Below: q.Below}, now)
	return &prepared, err
}
