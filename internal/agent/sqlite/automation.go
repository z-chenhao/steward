package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/event"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

// ManageAutomation is the trusted CLI entry. Model callers use the same
// transaction under their accepted ToolInvocation and Main claim.
func (r *Runs) ManageAutomation(ctx context.Context, key string, q automation.Request, now time.Time) (tool.Result, error) {
	result := tool.Result{}
	if key == "" || len(key) > 256 {
		return result, automation.ErrInvalid
	}
	raw, _ := encoded(q)
	if _, e := automation.DecodeRequest(raw); e != nil {
		return result, e
	}
	var plan *ownerSchedulePlan
	if q.Operation == "create" || q.Operation == "update" {
		prepared, err := r.prepareOwnerScheduleRequest(ctx, key, digest(raw), automation.OwnerConfiguration{Instruction: q.Instruction, Ref: q.Ref, Version: q.Version, Enabled: true, Type: q.Type, Spec: q.Spec, Timezone: q.Timezone}, now, false)
		if err != nil {
			return result, err
		}
		if prepared.invalid {
			return result, automation.ErrInvalid
		}
		plan = &prepared
	}
	err := r.write(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = r.manageAutomation(ctx, tx, key, q, now, plan)
		return e
	})
	return result, err
}
func (r *Runs) manageAutomation(ctx context.Context, tx *sql.Tx, key string, q automation.Request, now time.Time, plan *ownerSchedulePlan) (tool.Result, error) {
	var owner, ready string
	if err := tx.QueryRowContext(ctx, `SELECT agent_id,setup_status FROM agent_config WHERE singleton=1`).Scan(&owner, &ready); err != nil {
		return tool.Result{}, err
	}
	if ready != "ready" {
		return tool.Result{}, agent.ErrSetup
	}
	if q.Operation == "list" {
		rows, e := tx.QueryContext(ctx, `SELECT automation_id,instruction,trigger_type,trigger_spec,timezone,enabled,version,next_fire_at,below_value,intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=automation.agent_id),0),json_extract(presentation_json,'$.instruction_label'),
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=automation.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-instruction-label/1') FROM automation WHERE agent_id=? ORDER BY created_at,automation_id LIMIT 50`, owner)
		if e != nil {
			return tool.Result{}, e
		}
		defer rows.Close()
		views := []automation.View{}
		for rows.Next() {
			var v automation.View
			var eligible, managedLabel bool
			var label sql.NullString
			var next, below sql.NullInt64
			if e = rows.Scan(&v.Ref, &v.Instruction, &v.Type, &v.Spec, &v.Timezone, &v.Enabled, &v.Version, &next, &below, &eligible, &label, &managedLabel); e != nil {
				return tool.Result{}, e
			}
			if next.Valid {
				t := time.UnixMicro(next.Int64).UTC()
				v.Next = &t
			}
			if below.Valid {
				v.Below = &below.Int64
			}
			if !eligible {
				// Retain only Host control metadata. Instruction, schedule and
				// threshold are revoked evidence even before any Task exists.
				v = automation.View{Ref: v.Ref, Enabled: v.Enabled, Version: v.Version}
			} else if label.Valid && validScheduleInstructionLabel(label.String) {
				v.Instruction = label.String
			} else if managedLabel {
				return tool.Result{}, rsi.ErrInvalid
			} else {
				v.Instruction = legacyAutomationInstructionLabel(v.Instruction)
			}
			views = append(views, v)
		}
		if e := rows.Err(); e != nil {
			return tool.Result{}, e
		}
		raw, e := encoded(views)
		return tool.Result{Text: raw}, e
	}
	canonical, _ := encoded(q)
	hash := digest(canonical)
	var priorHash, prior string
	err := tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM automation_request WHERE agent_id=? AND request_key=?`, owner, key).Scan(&priorHash, &prior)
	if err == nil {
		if hash != priorHash {
			return tool.Result{}, agent.ErrConflict
		}
		var result tool.Result
		err = json.Unmarshal([]byte(prior), &result)
		return result, err
	}
	if err != sql.ErrNoRows {
		return tool.Result{}, err
	}
	if plan != nil {
		managed, err := scheduleActivatedTx(ctx, tx)
		if err != nil {
			return tool.Result{}, err
		}
		if plan.claimFence == nil && managed != plan.managed {
			return tool.Result{}, agent.ErrConflict
		}
		if plan.managed {
			if err = r.ownerScheduleFence(ctx, tx, *plan); err != nil {
				return tool.Result{}, err
			}
		}
	}
	next := func() (time.Time, error) {
		if plan != nil {
			if plan.invalid {
				return time.Time{}, automation.ErrInvalid
			}
			return plan.next, nil
		}
		// Only historical model Runs without the managed service reach this path.
		return automation.HistoricalNext(q.Type, q.Spec, q.Timezone, now)
	}
	var graph any
	if plan != nil && plan.managed {
		graph = plan.graph
	}
	var frequency, local, presentation any
	if plan != nil {
		if plan.presentation != nil {
			presentation, _ = encoded(plan.presentation)
		}
		if plan.projectedFrequency != nil {
			frequency, _ = encoded(plan.projectedFrequency)
		}
		if plan.projectedLocal != nil {
			local, _ = encoded(plan.projectedLocal)
		}
	}
	result := tool.Result{}
	id := q.Ref
	if q.Operation == "create" {
		t, e := next()
		if e != nil || !t.After(now) {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		id = "automation:" + newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version,below_value,intent_privacy_epoch,method_graph,owner_frequency_json,owner_local_json,presentation_json) VALUES(?,?,?,?,?,?,1,?,?,?,1,?,coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0),?,?,?,?)`, id, owner, q.Instruction, q.Type, q.Spec, q.Timezone, t.UnixMicro(), now.UnixMicro(), now.UnixMicro(), q.Below, owner, graph, frequency, local, presentation)
	} else {
		var version int64
		var taskID sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT version,task_id FROM automation WHERE automation_id=? AND agent_id=?`, id, owner).Scan(&version, &taskID)
		if err == sql.ErrNoRows {
			return tool.Failure("NOT_FOUND"), nil
		}
		if err != nil {
			return result, err
		}
		if version != q.Version {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		if q.Operation == "cancel" {
			_, err = tx.ExecContext(ctx, `UPDATE automation SET enabled=0,next_fire_at=NULL,schedule_version=schedule_version+1,updated_at=?,version=version+1 WHERE automation_id=?`, now.UnixMicro(), id)
			if err == nil && taskID.Valid {
				var status string
				var epoch, v int64
				err = tx.QueryRowContext(ctx, `SELECT status,task_epoch,version FROM task WHERE task_id=?`, taskID.String).Scan(&status, &epoch, &v)
				if err == nil && (status == "active" || status == "paused") {
					err = r.stopTask(ctx, tx, taskID.String, "cancelled", epoch, v)
				}
			}
		} else {
			t, e := next()
			if e != nil || !t.After(now) {
				return tool.Failure("INVALID_ARGUMENT"), nil
			}
			var status string
			if taskID.Valid {
				if e = tx.QueryRowContext(ctx, `SELECT status FROM task WHERE task_id=?`, taskID.String).Scan(&status); e != nil {
					return result, e
				}
				if status != "active" {
					return tool.Failure("INVALID_ARGUMENT"), nil
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE automation SET instruction=?,trigger_type=?,trigger_spec=?,timezone=?,is_below=CASE WHEN below_value IS ? THEN is_below ELSE 0 END,below_value=?,enabled=1,next_fire_at=?,method_graph=?,owner_frequency_json=?,owner_local_json=?,presentation_json=?,schedule_version=schedule_version+1,updated_at=?,version=version+1 WHERE automation_id=?`, q.Instruction, q.Type, q.Spec, q.Timezone, q.Below, q.Below, t.UnixMicro(), graph, frequency, local, presentation, now.UnixMicro(), id)
		}
	}
	if err != nil {
		return result, err
	}
	raw, _ := encoded(struct {
		Ref      string `json:"automationRef"`
		Accepted bool   `json:"accepted"`
	}{id, true})
	result.Text = raw
	saved, _ := encoded(result)
	if _, err = tx.ExecContext(ctx, `INSERT INTO automation_request VALUES(?,?,?,?)`, owner, key, hash, saved); err != nil {
		return result, err
	}
	return result, automationEvent(ctx, tx, id, "changed")
}

func (r *Runs) admitAutomations(ctx context.Context) error {
	var pending bool
	if err := r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_fire WHERE state='pending')`).Scan(&pending); err != nil {
		return agent.ErrStorage
	}
	if !pending {
		return nil
	}
	labels, err := r.prepareAutomationTaskLabels(ctx)
	if err != nil {
		return err
	}
	r.host.boundary("before_automation_label_commit")
	err = r.write(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT f.event_id FROM automation_fire f JOIN automation a USING(automation_id) WHERE f.state='pending' AND a.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) ORDER BY f.fire_at,f.event_id`)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			var automationID, brief string
			var enabled, revokedIntent bool
			var current, v, fireAt int64
			var taskID sql.NullString
			var below sql.NullInt64
			if e = tx.QueryRowContext(ctx, `SELECT a.automation_id,f.instruction,a.enabled,a.schedule_version,f.schedule_version,a.task_id,f.fire_at,f.below_value,a.intent_privacy_epoch!=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=a.agent_id),0) FROM automation_fire f JOIN automation a USING(automation_id) WHERE f.event_id=?`, id).Scan(&automationID, &brief, &enabled, &current, &v, &taskID, &fireAt, &below, &revokedIntent); e != nil {
				return e
			}
			// Intent provenance is frozen before the first Task exists. Neither
			// fire bookkeeping nor a fresh Task timestamp can renew revoked context.
			skip := !enabled || current != v || revokedIntent
			var epoch int64 = 1
			if taskID.Valid && !skip {
				var status string
				var revoked bool
				// createRoot rejects pre-Forget Task context. Consume that known
				// ineligibility here so a durable pending fire cannot block Next.
				if e = tx.QueryRowContext(ctx, `SELECT t.status,t.task_epoch,t.created_at<=coalesce(p.updated_at,0) FROM task t LEFT JOIN memory_privacy p USING(agent_id) WHERE t.task_id=?`, taskID.String).Scan(&status, &epoch, &revoked); e != nil {
					return e
				}
				skip = status != "active" || revoked
				var busy bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run WHERE task_id=? AND status IN ('queued','running','waiting'))`, taskID.String).Scan(&busy); e != nil {
					return e
				}
				if busy && !skip {
					continue
				}
			}
			if skip {
				if _, e = tx.ExecContext(ctx, `UPDATE automation_fire SET state='skipped' WHERE event_id=?`, id); e != nil {
					return e
				}
			} else {
				if !taskID.Valid {
					label, ok := labels[id]
					if !ok {
						continue
					}
					if e = r.automationTaskLabelFence(ctx, tx, label); e != nil {
						return e
					}
					var labelGraph any
					if label.managed {
						labelGraph = label.graph
					}
					if _, e = tx.ExecContext(ctx, `UPDATE automation_fire SET task_title=?,task_label_graph=?,task_label_managed=? WHERE event_id=?`, label.title, labelGraph, label.managed, id); e != nil {
						return e
					}
					taskID = sql.NullString{String: newID(), Valid: true}
					var owner string
					if e = tx.QueryRowContext(ctx, `SELECT agent_id FROM automation WHERE automation_id=?`, automationID).Scan(&owner); e != nil {
						return e
					}
					now := time.Now().UnixMicro()
					if _, e = tx.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy) VALUES(?,?,?,?,?,?,'[]','active',1,?,?,1,'ongoing')`, taskID.String, "task:"+newID(), owner, owner, label.title, brief, now, now); e != nil {
						return e
					}
					if _, e = tx.ExecContext(ctx, `UPDATE automation SET task_id=?,version=version+1 WHERE automation_id=?`, taskID.String, automationID); e != nil {
						return e
					}
				}
				instruction := fmt.Sprintf("Automation scheduled occurrence: %s.\n%s", time.UnixMicro(fireAt).UTC().Format(time.RFC3339), brief)
				if below.Valid {
					guidance, err := r.automationObservationInstruction(ctx, tx, below.Int64)
					if err != nil {
						return err
					}
					instruction += "\n" + guidance
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) VALUES(?,?,?,?,'[]',?)`, newID(), taskID.String, epoch, instruction, time.Now().UnixMicro()); e != nil {
					return e
				}
				if e = r.createRoot(ctx, tx, taskID.String); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `UPDATE automation_fire SET state='admitted',root_run_id=(SELECT run_id FROM run WHERE task_id=? AND worker_position='root' AND status='queued') WHERE event_id=?`, taskID.String, id); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO event_inbox(consumer,event_id,processed_at) VALUES('automation_actor',?,?)`, id, time.Now().UnixMicro()); e != nil {
				return e
			}
		}
		r.host.boundary("before_automation_admit_commit")
		return nil
	})
	if err == nil {
		r.host.boundary("after_automation_admit_commit")
	}
	return err
}

func (t *tools) executeAutomation(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	result := tool.Failure("PERMISSION_DENIED")
	now := t.runs.AutomationNow()
	plan, err := t.prepareMainSchedule(ctx, inv, now)
	if err != nil {
		return result, err
	}
	threshold, err := t.prepareScheduleThreshold(ctx, inv)
	if err != nil {
		return result, err
	}
	if threshold != nil {
		t.runs.host.boundary("before_automation_observe_commit")
	}
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if e := t.fence(ctx, tx); e != nil {
			return e
		}
		var name, args, kind string
		if e := tx.QueryRowContext(ctx, `SELECT i.tool_name,i.arguments_json,r.kind FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=? AND i.run_id=? AND i.status='running'`, inv.ID, t.claim.RunID()).Scan(&name, &args, &kind); e != nil {
			return e
		}
		if name == automation.ManageName && kind == "main" {
			q, e := automation.DecodeRequest(args)
			if e != nil {
				result = tool.Failure("INVALID_ARGUMENT")
				return nil
			}
			var source string
			if e = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT message_id FROM main_input WHERE run_id=r.run_id AND sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY sequence DESC LIMIT 1),'') FROM run r WHERE r.run_id=?`, t.claim.RunID()).Scan(&source); e != nil {
				return e
			}
			if source == "" {
				source = inv.ID
			}
			canonical, _ := encoded(q)
			result, e = t.runs.manageAutomation(ctx, tx, source+":"+digest(canonical), q, now, plan)
			return e
		}
		if name != automation.ObserveName || !tool.Validate(automation.ObserveSchema, args) {
			return nil
		}
		var q struct {
			Value int64 `json:"value"`
		}
		if json.Unmarshal([]byte(args), &q) != nil {
			return nil
		}
		var id string
		var below, old sql.NullInt64
		var at int64
		var entered bool
		e := tx.QueryRowContext(ctx, `SELECT f.event_id,f.below_value,f.observed_value,f.fire_at,a.is_below FROM automation_fire f JOIN automation a USING(automation_id) JOIN run r ON r.run_id=f.root_run_id WHERE f.root_run_id=? AND a.enabled=1 AND a.schedule_version=f.schedule_version AND f.below_value IS NOT NULL AND r.worker_position='root'`, t.claim.RunID()).Scan(&id, &below, &old, &at, &entered)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		if old.Valid {
			if old.Int64 != q.Value {
				result = tool.Failure("NON_DETERMINISTIC_REPLAY")
				return nil
			}
		} else {
			var latest int64
			if e = tx.QueryRowContext(ctx, `SELECT a.last_observed_fire FROM automation a JOIN automation_fire f USING(automation_id) WHERE f.event_id=?`, id).Scan(&latest); e != nil {
				return e
			}
			var isBelow, notify bool
			if threshold != nil {
				if e = t.scheduleThresholdFence(ctx, tx, inv, *threshold); e != nil {
					return e
				}
				isBelow, notify = threshold.decision.IsBelow, threshold.decision.Notify
			} else {
				var managed bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run b,json_each(b.managed_services_json) s WHERE b.run_id=? AND s.value=?)`, t.claim.RunID(), contract.Threshold).Scan(&managed); e != nil {
					return e
				}
				if managed {
					return rsi.ErrInvalid
				}
				isBelow, notify = legacyAutomationThreshold(q.Value, below.Int64, entered, at > latest)
			}
			if _, e = tx.ExecContext(ctx, `UPDATE automation_fire SET observed_value=?,notify=? WHERE event_id=?`, q.Value, notify, id); e != nil {
				return e
			}
			if at > latest {
				if _, e = tx.ExecContext(ctx, `UPDATE automation SET is_below=?,last_observed_fire=?,version=version+1 WHERE automation_id=(SELECT automation_id FROM automation_fire WHERE event_id=?)`, isBelow, at, id); e != nil {
					return e
				}
			}
		}
		result = tool.Result{Text: `{"accepted":true}`}
		if !old.Valid {
			return automationEvent(ctx, tx, id, "observed")
		}
		return nil
	})
	return result, err
}

func (r *Runs) AutomationNow() time.Time {
	if r.automationClock != nil {
		return r.automationClock()
	}
	return time.Now()
}
func (r *Runs) SetTestAutomationClock(now time.Time) {
	r.automationClock = func() time.Time { return now }
}

func automationEvent(ctx context.Context, tx *sql.Tx, id, kind string) error {
	e, err := event.New("/steward/automations", "ai.steward.automation."+kind+".v1", id, struct {
		ID string `json:"id"`
	}{id})
	if err != nil {
		return err
	}
	return eventsqlite.Enqueue(ctx, tx, e)
}
