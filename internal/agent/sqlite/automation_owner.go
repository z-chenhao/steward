package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
)

// ConfigureAutomation uses the same durable receipt/version/fire mechanism as
// trusted CLI management, with reversible disabling instead of Task cancellation.
// It never modifies an admitted fire, Task instruction or frozen Run snapshot.
func (r *Runs) ConfigureAutomation(ctx context.Context, key string, q automation.OwnerConfiguration, now time.Time) (automation.OwnerReceipt, error) {
	var result automation.OwnerReceipt
	if key == "" || len(key) > 220 || q.ValidateShape() != nil {
		return result, automation.ErrInvalid
	}
	plan, err := r.prepareOwnerSchedule(ctx, key, q, now)
	if err != nil {
		return result, err
	}
	err = r.write(ctx, func(tx *sql.Tx) error {
		var owner, ready string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id,setup_status FROM agent_config WHERE singleton=1`).Scan(&owner, &ready); err != nil {
			return err
		}
		if ready != "ready" {
			return agent.ErrSetup
		}
		raw, _ := encoded(q)
		hash := digest("owner-configuration:" + raw)
		requestKey := "owner-config:" + key
		var savedHash, saved string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM automation_request WHERE agent_id=? AND request_key=?`, owner, requestKey).Scan(&savedHash, &saved)
		if err == nil {
			if savedHash != hash && !historicalOwnerScheduleReceipt(q, savedHash) {
				return agent.ErrConflict
			}
			return json.Unmarshal([]byte(saved), &result)
		}
		if err != sql.ErrNoRows {
			return err
		}
		if !plan.localDisable {
			managed, err := scheduleActivatedTx(ctx, tx)
			if err != nil {
				return err
			}
			if managed != plan.managed {
				return agent.ErrConflict
			}
		}
		if plan.managed {
			if err := r.ownerScheduleFence(ctx, tx, plan); err != nil {
				return err
			}
		}
		var methodGraph any
		if plan.managed {
			methodGraph = plan.graph
		}
		if !plan.localDisable && plan.invalid {
			return automation.ErrInvalid
		}
		spec := q.Spec
		if q.LocalDate != "" || q.Frequency != nil {
			spec = plan.resolvedSpec
		}
		var frequency any
		if q.Frequency != nil {
			frequency, _ = encoded(q.Frequency)
		} else if plan.projectedFrequency != nil {
			frequency, _ = encoded(plan.projectedFrequency)
		}
		var local any
		if q.LocalDate != "" {
			local, _ = encoded(automation.OwnerLocal{Date: q.LocalDate, Clock: q.LocalClock})
		} else if plan.projectedLocal != nil {
			local, _ = encoded(plan.projectedLocal)
		}
		var presentation any
		if plan.presentation != nil {
			presentation, _ = encoded(plan.presentation)
		}
		var next any
		if q.Enabled {
			if plan.invalid || !plan.next.After(now) {
				return automation.ErrInvalid
			}
			next = plan.next.UnixMicro()
		}
		if q.Ref == "" {
			result = automation.OwnerReceipt{Ref: "automation:" + newID(), Version: 1}
			_, err = tx.ExecContext(ctx, `INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version,below_value,intent_privacy_epoch,method_graph,owner_frequency_json,owner_local_json,presentation_json) VALUES(?,?,?,?,?,?,?,?,?,?,1,?,coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0),?,?,?,?)`, result.Ref, owner, q.Instruction, q.Type, spec, q.Timezone, q.Enabled, next, now.UnixMicro(), now.UnixMicro(), q.Below, owner, methodGraph, frequency, local, presentation)
		} else {
			var version int64
			var eligible bool
			var taskID sql.NullString
			var priorSpec, priorZone string
			err = tx.QueryRowContext(ctx, `SELECT version,task_id,intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=automation.agent_id),0),trigger_spec,timezone FROM automation WHERE automation_id=? AND agent_id=?`, q.Ref, owner).Scan(&version, &taskID, &eligible, &priorSpec, &priorZone)
			if err == sql.ErrNoRows {
				return agent.ErrConflict
			}
			if err != nil {
				return err
			}
			if version != q.Version || !eligible {
				return agent.ErrConflict
			}
			if q.PreserveSpec != "" && (q.PreserveSpec != priorSpec || q.Timezone != priorZone) {
				return agent.ErrConflict
			}
			if taskID.Valid && q.Enabled {
				var status string
				if err = tx.QueryRowContext(ctx, `SELECT status FROM task WHERE task_id=? AND agent_id=?`, taskID.String, owner).Scan(&status); err != nil {
					return err
				}
				if status != "active" {
					return agent.ErrConflict
				}
			}
			result = automation.OwnerReceipt{Ref: q.Ref, Version: version + 1}
			_, err = tx.ExecContext(ctx, `UPDATE automation SET instruction=?,trigger_type=?,trigger_spec=?,timezone=?,enabled=?,next_fire_at=?,is_below=CASE WHEN below_value IS ? THEN is_below ELSE 0 END,below_value=?,schedule_version=schedule_version+1,method_graph=CASE WHEN ? THEN ? ELSE method_graph END,owner_frequency_json=CASE WHEN ? THEN owner_frequency_json ELSE ? END,owner_local_json=CASE WHEN ? THEN owner_local_json ELSE ? END,presentation_json=CASE WHEN ? THEN presentation_json ELSE ? END,updated_at=?,version=version+1 WHERE automation_id=? AND agent_id=? AND version=?`, q.Instruction, q.Type, spec, q.Timezone, q.Enabled, next, q.Below, q.Below, !plan.localDisable, methodGraph, plan.localDisable, frequency, plan.localDisable, local, plan.localDisable, presentation, now.UnixMicro(), q.Ref, owner, version)
		}
		if err != nil {
			return err
		}
		saved, _ = encoded(result)
		if _, err = tx.ExecContext(ctx, `INSERT INTO automation_request(agent_id,request_key,request_hash,result_json) VALUES(?,?,?,?)`, owner, requestKey, hash, saved); err != nil {
			return err
		}
		return automationEvent(ctx, tx, result.Ref, "changed")
	})
	if err == automation.ErrInvalid && plan.minimumBelow > 0 {
		return result, automation.ThresholdMinimumError{Minimum: plan.minimumBelow}
	}
	return result, err
}
