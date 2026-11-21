package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
)

// TrialAutomation admits a single, privately scoped occurrence. Its separate
// observation domain lets the normal Root use the configured threshold while
// preserving the live plan's observations, notification history and next time.
func (r *Runs) TrialAutomation(ctx context.Context, key, ref string, version int64) (string, error) {
	if len(key) != 32 || strings.Trim(key, "0123456789abcdef") != "" || !strings.HasPrefix(ref, "automation:") || len(ref) > 256 || version < 1 {
		return "", automation.ErrInvalid
	}
	var result string
	err := r.write(ctx, func(tx *sql.Tx) error {
		var owner, brief, graph, savedGraph, label string
		var current, privacy int64
		var below sql.NullInt64
		var entered bool
		err := tx.QueryRowContext(ctx, `SELECT a.agent_id,a.instruction,a.version,a.intent_privacy_epoch,a.below_value,a.is_below,
coalesce(a.method_graph,''),coalesce(c.active_graph,''),coalesce(json_extract(a.presentation_json,'$.instruction_label'),'')
FROM automation a JOIN agent_config owner ON owner.agent_id=a.agent_id AND owner.singleton=1 AND owner.setup_status='ready'
JOIN rsi_control c ON c.singleton=1 LEFT JOIN memory_privacy m ON m.agent_id=a.agent_id
WHERE a.automation_id=? AND a.intent_privacy_epoch=coalesce(m.epoch,0)
AND NOT EXISTS(SELECT 1 FROM task t WHERE t.task_id=a.task_id AND (t.status!='active' OR t.created_at<=coalesce(m.updated_at,0)))
AND EXISTS(SELECT 1 FROM channel_binding b WHERE b.channel='web' AND b.subject=? AND b.destination=? AND b.enabled=1)`, ref, interaction.WebSubject, interaction.WebDestination).Scan(&owner, &brief, &current, &privacy, &below, &entered, &savedGraph, &graph, &label)
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrConflict
		}
		if err != nil {
			return err
		}
		requestKey := "owner-trial:" + key
		hash := digest(ref + "@" + strconv.FormatInt(version, 10))
		var savedHash, saved string
		err = tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM automation_request WHERE agent_id=? AND request_key=?`, owner, requestKey).Scan(&savedHash, &saved)
		if err == nil {
			if savedHash != hash {
				return agent.ErrConflict
			}
			return json.Unmarshal([]byte(saved), &result)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != version {
			return agent.ErrConflict
		}
		if graph == "" {
			return rsi.ErrNotFound
		}
		if err = checkScheduleGraphTx(ctx, tx, graph); err != nil {
			return err
		}
		if savedGraph != "" && savedGraph != graph {
			if err = checkScheduleGraphTx(ctx, tx, savedGraph); err != nil {
				return err
			}
		}
		guidance, _, err := r.readScheduleGuidance(ctx, tx)
		if err != nil {
			return err
		}
		if guidance == nil || below.Valid && guidance.Observation == nil {
			return rsi.ErrNotFound
		}
		if label == "" {
			label = brief
		}
		now := time.Now().UTC()
		id := newID()
		result = "task:" + newID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy)
VALUES(?,?,?,?,?,?,'[]','active',1,?,?,1,'once')`, id, result, owner, owner, task.Title("Trial · "+label), brief, now.UnixMicro(), now.UnixMicro()); err != nil {
			return err
		}
		trial := "trial:" + id
		if _, err = tx.ExecContext(ctx, `INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,created_at,updated_at,version,task_id,below_value,is_below,intent_privacy_epoch,method_graph)
VALUES(?,?,?,'at',?,'UTC',1,?,?,1,?,?,?,?,?)`, trial, owner, brief, now.Format(time.RFC3339Nano), now.UnixMicro(), now.UnixMicro(), id, below, entered, privacy, graph); err != nil {
			return err
		}
		if below.Valid {
			instruction, err := r.automationObservationInstruction(ctx, tx, below.Int64)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) VALUES(?,?,1,?,'[]',?)`, newID(), id, instruction, now.UnixMicro()); err != nil {
				return err
			}
		}
		if err = r.createRoot(ctx, tx, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO automation_fire(event_id,automation_id,schedule_version,fire_at,instruction,below_value,state,method_graph,root_run_id)
SELECT ?,?,1,?,?,?,'admitted',?,run_id FROM run WHERE task_id=? AND worker_position='root'`, newID(), trial, now.UnixMicro(), brief, below, graph, id); err != nil {
			return err
		}
		saved, _ = encoded(result)
		_, err = tx.ExecContext(ctx, `INSERT INTO automation_request(agent_id,request_key,request_hash,result_json) VALUES(?,?,?,?)`, owner, requestKey, hash, saved)
		return err
	})
	return result, err
}
