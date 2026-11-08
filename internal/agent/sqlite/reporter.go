package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/event"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (r *Runs) searchTasks(ctx context.Context, tx *sql.Tx, runID string, request task.SearchRequest) (tool.Result, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.task_ref,t.title,t.anchor_task,t.status,t.progress_summary,t.updated_at,coalesce((SELECT group_concat(i.instruction,char(10)) FROM task_instruction i WHERE i.task_id=t.task_id),'') FROM task t JOIN run r ON t.agent_id=r.agent_id AND t.conversation_id=r.conversation_id WHERE r.run_id=? AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0) ORDER BY t.updated_at DESC,t.task_ref`, runID)
	if err != nil {
		return tool.Result{}, err
	}
	defer rows.Close()
	candidates := []task.Candidate{}
	recent := 0
	for rows.Next() {
		var s task.Summary
		var instructions string
		var updated int64
		if err = rows.Scan(&s.TaskRef, &s.Title, &s.GoalSummary, &s.Status, &s.ProgressSummary, &updated, &instructions); err != nil {
			return tool.Result{}, err
		}
		if request.Scope == "active" && s.Status != "active" {
			continue
		}
		if request.Scope == "recent" {
			if updated < time.Now().Add(-r.recentAge).UnixMicro() || recent >= r.recentLimit {
				continue
			}
			recent++
		}
		match := len(request.Status) == 0
		for _, status := range request.Status {
			if status == s.Status {
				match = true
			}
		}
		if !match {
			continue
		}
		s.UpdatedAt = time.UnixMicro(updated).UTC().Format("2006-01-02T15:04:05.000000Z")
		text := s.Title + "\n" + s.GoalSummary + "\n" + instructions
		// Search returns a bounded goal summary, not the full private execution context.
		rs := []rune(s.GoalSummary)
		if len(rs) > 240 {
			s.GoalSummary = string(rs[:240]) + "…"
		}
		candidates = append(candidates, task.Candidate{Summary: task.BoundSummary(s), Text: text})
	}
	if err = rows.Err(); err != nil {
		return tool.Result{}, err
	}
	raw, err := encoded(task.Rank(candidates, request.Query, request.Limit))
	return tool.Result{Text: raw}, err
}

// finishTask participates in the terminal Run transaction. Progress never enters
// this table, and a failed Root leaves its Task active for an explicit append.
func (r *Runs) finishTask(ctx context.Context, tx *sql.Tx, runID, status, reason string) error {
	var kind string
	var taskID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT kind,task_id FROM run WHERE run_id=?`, runID).Scan(&kind, &taskID); err != nil {
		return err
	}
	if kind != "worker" {
		return nil
	}
	var rootID string
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT root_run_id,task_epoch FROM run WHERE run_id=?`, runID).Scan(&rootID, &epoch); err != nil {
		return err
	}
	// A Root may have finished while descendants still drain. Only the final
	// tree boundary completes the Task and publishes the Root's result.
	if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(error_code,'') FROM run WHERE run_id=?`, rootID).Scan(&status, &reason); err != nil {
		return err
	}
	if status != "completed" && status != "failed" {
		return nil
	}
	runID = rootID
	var staleAutomation bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_fire f JOIN automation a USING(automation_id) WHERE f.root_run_id=? AND (a.enabled=0 OR a.schedule_version!=f.schedule_version))`, runID).Scan(&staleAutomation); err != nil {
		return err
	}
	if staleAutomation {
		return nil
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE task_id=? AND task_epoch=? AND run_id!=? AND status IN ('queued','running','waiting')`, taskID.String, epoch, runID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	var reported int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_update WHERE root_run_id=?`, runID).Scan(&reported); err != nil || reported > 0 {
		return err
	}
	now := time.Now().UnixMicro()
	content := "Worker execution failed: " + reason
	if status == "completed" {
		if err := tx.QueryRowContext(ctx, `SELECT content FROM worker_result WHERE run_id=? ORDER BY created_at DESC,exchange_id DESC LIMIT 1`, runID).Scan(&content); err != nil {
			return err
		}
	}
	// A completed model loop cannot establish an unconfirmed external effect.
	// Keep the Task active and report the existing unknown result kind to Main.
	var unknown int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tool_invocation i JOIN run r USING(run_id) WHERE r.task_id=? AND r.task_epoch=? AND i.status='unknown'`, taskID.String, epoch).Scan(&unknown); err != nil {
		return err
	}
	if unknown > 0 {
		status = "unknown"
		content = "External effect outcome is unconfirmed. " + content
	}
	next := "active"
	var completed any
	var policy string
	if err := tx.QueryRowContext(ctx, `SELECT completion_policy FROM task WHERE task_id=?`, taskID.String).Scan(&policy); err != nil {
		return err
	}
	if status == "completed" && policy == "once" && pending == 0 {
		next = "completed"
		completed = now
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM task WHERE task_id=? AND task_epoch=? AND status='active'`, taskID.String, epoch).Scan(&version); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE task SET status=?,completed_at=?,updated_at=?,version=version+1 WHERE task_id=? AND task_epoch=? AND version=? AND status='active'`, next, completed, now, taskID.String, epoch, version)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return agent.ErrClaimLost
	}
	if status == "completed" {
		var monitored, suppressed, missing bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_fire WHERE root_run_id=? AND below_value IS NOT NULL),EXISTS(SELECT 1 FROM automation_fire WHERE root_run_id=? AND below_value IS NOT NULL AND observed_value IS NOT NULL AND notify=0),EXISTS(SELECT 1 FROM automation_fire WHERE root_run_id=? AND below_value IS NOT NULL AND observed_value IS NULL)`, runID, runID, runID).Scan(&monitored, &suppressed, &missing); err != nil {
			return err
		}
		if monitored && suppressed {
			return nil
		}
		if missing {
			status = "needs_input"
			content = "Automation could not establish an observation. " + content
		}
	}
	if status == "failed" {
		var title string
		if err := tx.QueryRowContext(ctx, `SELECT title FROM task WHERE task_id=?`, taskID.String).Scan(&title); err != nil {
			return err
		}
		// A model follow-up can omit or misattribute a failure. The Host owns
		// this bounded lifecycle notice and the existing source-bound delivery.
		text := fmt.Sprintf("Task %q did not finish. This attempt failed; the task remains open. You can ask to continue it.", task.Title(title))
		if err := r.notification(ctx, tx, runID, "failure:"+runID, "failure", "task", text); err != nil {
			return err
		}
	}
	return r.taskUpdate(ctx, tx, taskID.String, runID, epoch, status, content)
}
func (r *Runs) taskUpdate(ctx context.Context, tx *sql.Tx, taskID, runID string, epoch int64, kind, content string) error {
	e, err := event.New("/steward/tasks", "ai.steward.task_update.v1", taskID, struct {
		TaskID    string `json:"task_id"`
		RootRunID string `json:"root_run_id"`
		Epoch     int64  `json:"task_epoch"`
		Kind      string `json:"kind"`
	}{taskID, runID, epoch, kind})
	if err != nil {
		return err
	}
	refs, err := published(ctx, tx, runID)
	if err != nil {
		return err
	}
	if kind == "completed" {
		var owner string
		if err = tx.QueryRowContext(ctx, `SELECT agent_id FROM task WHERE task_id=?`, taskID).Scan(&owner); err != nil {
			return err
		}
		if err = r.observeMemory(ctx, tx, owner, "task", runID); err != nil {
			return err
		}
	}
	rawRefs, _ := encoded(refs)
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_update VALUES(?,?,?,?,?,?,?,?)`, e.ID(), taskID, runID, epoch, kind, content, rawRefs, e.Time().UnixMicro()); err != nil {
		return err
	}
	return eventsqlite.Enqueue(ctx, tx, e)
}

// A Task epoch does not change when its Automation schedule is edited. Keep
// the immutable update -> Root -> fire lineage through both pending delivery
// boundaries. Ordinary Task updates have no fire and remain eligible.
const currentAutomationUpdate = `NOT EXISTS(SELECT 1 FROM automation_fire f JOIN automation a USING(automation_id) WHERE f.root_run_id=u.root_run_id AND (a.enabled=0 OR a.schedule_version!=f.schedule_version))`

// Report reconstructs from authoritative updates, even if a queue wake was
// lost. Inbox and consumer dedup commit together; repeating it is harmless.
// Stale epochs and schedule versions are consumed but never enter a new Main.
func (r *Runs) Report(ctx context.Context) error {
	changed := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		changed = false
		rows, err := tx.QueryContext(ctx, `SELECT u.event_id,u.task_id,u.root_run_id,u.task_epoch,u.kind,u.content,u.artifact_refs,u.occurred_at,t.agent_id,t.task_ref,t.title,CASE WHEN t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0) AND `+currentAutomationUpdate+` THEN t.task_epoch ELSE -1 END FROM task_update u JOIN task t USING(task_id) WHERE t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND NOT EXISTS(SELECT 1 FROM event_inbox i WHERE i.consumer='task_reporter' AND i.event_id=u.event_id) ORDER BY u.occurred_at,u.event_id`)
		if err != nil {
			return err
		}
		type update struct {
			eventID, taskID, rootID, kind, content, refs, agentID, taskRef, title string
			epoch, current, at                                                    int64
		}
		updates := []update{}
		for rows.Next() {
			var u update
			if err = rows.Scan(&u.eventID, &u.taskID, &u.rootID, &u.epoch, &u.kind, &u.content, &u.refs, &u.at, &u.agentID, &u.taskRef, &u.title, &u.current); err != nil {
				rows.Close()
				return err
			}
			updates = append(updates, u)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, u := range updates {
			if u.current == u.epoch {
				at := time.UnixMicro(u.at).UTC()
				var refs []string
				if json.Unmarshal([]byte(u.refs), &refs) != nil {
					return agent.ErrStorage
				}
				message := taskMessage(u.eventID, u.taskRef, u.kind, u.content, refs, at)
				if u.kind == "failed" {
					message.Details.Title = task.Title(u.title)
				}
				raw, err := encoded(message)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO main_inbox(event_id,request_id,agent_id,message_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(event_id) DO NOTHING`, u.eventID, message.MessageID, u.agentID, raw, at.UnixMicro()); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO event_inbox VALUES('task_reporter',?,?) ON CONFLICT DO NOTHING`, u.eventID, time.Now().UnixMicro()); err != nil {
				return err
			}
		}
		if len(updates) > 0 {
			changed = true
			r.host.boundary("before_report_commit")
		}
		return nil
	})
	if err == nil && changed {
		r.host.boundary("after_report_commit")
	}
	return err
}

// promoteFollowups admits one compatible original-source group into a new Main,
// only after the active Main has ended. Other groups remain durable pending work.
// No progress or Worker text enters the
// user Conversation; only Main's eventual answer uses the normal delivery path.
func (r *Runs) promoteFollowups(ctx context.Context) error {
	created := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE kind='main' AND status IN ('queued','running','waiting')`).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT b.sequence,b.message_json,u.task_id FROM main_inbox b JOIN task_update u USING(event_id) JOIN task t USING(task_id) WHERE b.run_id IS NULL AND b.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND u.task_epoch=t.task_epoch AND `+currentAutomationUpdate+` AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0) ORDER BY b.sequence`)
		if err != nil {
			return err
		}
		ids := []int64{}
		messages := []agent.AgentMessage{}
		taskIDs := []string{}
		for rows.Next() {
			var id int64
			var raw string
			var taskID string
			var message agent.AgentMessage
			if err = rows.Scan(&id, &raw, &taskID); err != nil {
				rows.Close()
				return err
			}
			if json.Unmarshal([]byte(raw), &message) != nil {
				rows.Close()
				return agent.ErrStorage
			}
			ids = append(ids, id)
			messages = append(messages, message)
			taskIDs = append(taskIDs, taskID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// One semantic reply must not combine unrelated original IM topics.
		// Leave the other groups in the same durable inbox for the next Main;
		// the existing single-Main constraint and recovery scanner remain owners.
		selected, err := taskReplyOrigin(ctx, tx, taskIDs[0])
		if err != nil {
			return err
		}
		groupIDs, groupMessages := []int64{}, []agent.AgentMessage{}
		for i, taskID := range taskIDs {
			origin, err := taskReplyOrigin(ctx, tx, taskID)
			if err != nil {
				return err
			}
			if origin == selected {
				groupIDs = append(groupIDs, ids[i])
				groupMessages = append(groupMessages, messages[i])
			}
		}
		ids, messages = groupIDs, groupMessages
		var agentID, profile string
		var config int64
		if err = tx.QueryRowContext(ctx, `SELECT agent_id,coalesce(active_model_profile_id,''),config_version FROM agent_config WHERE singleton=1 AND setup_status='ready'`).Scan(&agentID, &profile, &config); err != nil {
			return agent.ErrSetup
		}
		id, err := r.createMain(ctx, tx, agentID, profile, config, messages)
		if err != nil {
			return err
		}

		for _, seq := range ids {
			if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_reference SELECT j.value,'run',? FROM main_inbox b JOIN task_update u USING(event_id),json_each(u.artifact_refs) j WHERE b.sequence=? ON CONFLICT DO NOTHING`, id, seq); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE main_inbox SET run_id=? WHERE sequence=? AND run_id IS NULL`, id, seq); err != nil {
				return err
			}
		}
		created = true
		r.host.boundary("before_followup_commit")
		return runEvent(ctx, tx, id, "main_followup.accepted", 1)
	})
	if err == nil && created {
		r.host.boundary("after_followup_commit")
	}
	return err
}

// ConfigureRecentTasks is composition-time policy. Run execution never changes
// it; recent is a search window, not a deletion or retention job.
func (r *Runs) ConfigureRecentTasks(age time.Duration, limit int) {
	r.recentAge = age
	r.recentLimit = limit
}

func taskMessage(eventID, ref, kind, content string, refs []string, at time.Time) agent.AgentMessage {
	message := agent.TextMessage("custom", content)
	message.MessageID = "report:" + eventID
	message.CustomType = "task_update"
	message.Origin = "runtime"
	message.CreatedAt = &at
	message.Details = &task.UpdateDetails{TaskRef: ref, Kind: kind, ArtifactRefs: refs}
	return message
}
