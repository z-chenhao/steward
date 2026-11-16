package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

// createMain is shared by user ingress and durable Reporter follow-ups. Both
// read current profile/config only when creating a new Run.
func (r *Runs) createMain(ctx context.Context, tx *sql.Tx, agentID, profile string, configVersion int64, updates []agent.AgentMessage) (string, error) {
	var pv int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM model_profile WHERE profile_id=? AND status='ready'`, profile).Scan(&pv); err != nil {
		return "", agent.ErrSetup
	}
	snapshot, err := r.host.Models().FreezeProfile(ctx, tx, profile, pv)
	if err != nil {
		return "", err
	}
	if err := r.freezeSpend(ctx, tx, snapshot); err != nil {
		return "", err
	}
	id, capID := newID(), newID()
	allSpecs, err := r.pluginSpecs(ctx, tx, r.registry.Describe())
	if err != nil {
		return "", err
	}
	mainSpecs := agent.MainTools(allSpecs)
	specs, _ := encoded(mainSpecs)
	now := time.Now().UnixMicro()
	if _, err = tx.ExecContext(ctx, `INSERT INTO capability_snapshot(snapshot_id,agent_config_version,role,tool_specs_hash,skill_set_hash,data_scope,can_spawn_agents,tools_json,created_at,format_version) VALUES(?,?,'main',?,?,?,0,?,?,2)`, capID, configVersion, digest(specs), skill.SetHash(mainSpecs), agentID, specs, now); err != nil {
		return "", err
	}
	cp, _ := encoded(agent.Checkpoint{ContextVersion: 2, SerialInputs: true, Phase: "model", Messages: append(agent.InitialContext(agent.ContextOptions{Position: "main", Tools: mainSpecs}), updates...)})
	if _, err = tx.ExecContext(ctx, `INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES(?,?,?,'main','queued',?,?,?,(SELECT coalesce(max(sequence),0) FROM conversation_intent),?,?,1)`, id, agentID, agentID, capID, snapshot.SnapshotID, cp, now, now); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO main_tool_protocol(run_id) VALUES(?)`, id); err != nil {
		return "", err
	}
	// History grants remain within this local user's Conversation. Later Main
	// Runs can reopen refs already supplied or explicitly delivered there.
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_reference SELECT json_extract(j.value,'$.artifact_ref'),'run',? FROM conversation_intent c,json_each(json_extract(c.message_json,'$.content')) j JOIN artifact a ON a.artifact_ref=json_extract(j.value,'$.artifact_ref') WHERE json_extract(c.message_json,'$.conversation_id')=? AND a.owner_agent_id=? ON CONFLICT DO NOTHING`, id, agentID, agentID); err != nil {
		return "", err
	}
	if err = r.freezeContext(ctx, tx, id); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO run_claim VALUES(?,0,NULL,0,1)`, id); err != nil {
		return "", err
	}
	return id, runEvent(ctx, tx, id, "run_ready", 1)
}

func (r *Runs) createRoot(ctx context.Context, tx *sql.Tx, taskID string) error {
	if err := r.bindTaskCode(ctx, tx, taskID); err != nil {
		return err
	}
	var revoked bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task t JOIN memory_privacy p USING(agent_id) WHERE t.task_id=? AND t.created_at<=p.updated_at)`, taskID).Scan(&revoked); e != nil {
		return e
	}
	if revoked {
		return agent.ErrConflict
	}
	var agentID, conversationID, title, brief, profile string
	var epoch, config, pv int64
	if err := tx.QueryRowContext(ctx, `SELECT t.agent_id,t.conversation_id,t.title,t.anchor_task,t.task_epoch,a.active_model_profile_id,a.config_version,p.version FROM task t JOIN agent_config a ON a.agent_id=t.agent_id JOIN model_profile p ON p.profile_id=a.active_model_profile_id WHERE t.task_id=? AND t.status='active' AND a.setup_status='ready' AND p.status='ready'`, taskID).Scan(&agentID, &conversationID, &title, &brief, &epoch, &profile, &config, &pv); err != nil {
		return agent.ErrSetup
	}
	snapshot, err := r.host.Models().FreezeProfile(ctx, tx, profile, pv)
	if err != nil {
		return err
	}
	if err := r.freezeSpend(ctx, tx, snapshot); err != nil {
		return err
	}
	var monitor bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation WHERE task_id=? AND enabled=1 AND below_value IS NOT NULL)`, taskID).Scan(&monitor); err != nil {
		return err
	}
	specs := []tool.Spec{}
	allSpecs, err := r.pluginSpecs(ctx, tx, r.registry.Describe())
	if err != nil {
		return err
	}
	for _, spec := range agent.WorkerTools(allSpecs) {
		if spec.Name != automation.ObserveName || monitor {
			specs = append(specs, spec)
		}
	}
	spawn := false
	for _, spec := range specs {
		spawn = spawn || spec.Name == agent.RunToolName
	}
	raw, _ := encoded(specs)
	id, capID := newID(), newID()
	now := time.Now().UnixMicro()
	if _, err = tx.ExecContext(ctx, `INSERT INTO capability_snapshot(snapshot_id,agent_config_version,role,tool_specs_hash,skill_set_hash,data_scope,can_spawn_agents,tools_json,created_at,format_version) VALUES(?,?,'worker',?,?,?,?,?,?,2)`, capID, config, digest(raw), skill.SetHash(specs), agentID, spawn, raw, now); err != nil {
		return err
	}
	var refs string
	if err = tx.QueryRowContext(ctx, `SELECT anchor_artifact_refs FROM task WHERE task_id=?`, taskID).Scan(&refs); err != nil {
		return err
	}
	var artifactRefs []string
	if json.Unmarshal([]byte(refs), &artifactRefs) != nil {
		return agent.ErrStorage
	}
	messages := agent.InitialContext(agent.ContextOptions{Position: "root", Tools: specs, Title: title, Task: brief, ArtifactRefs: artifactRefs})
	var codeWorkspace int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_code_environment WHERE task_id=?`, taskID).Scan(&codeWorkspace); err != nil {
		return err
	}
	if codeWorkspace > 0 {
		messages = slices.Insert(messages, len(messages)-1, agent.RuntimeContext("task_code_environment", "", false, "This Task has a dedicated repository working copy. File and bash tools operate in that copy; use relative paths and read its AGENTS.md first. The import baseline includes owner dirty files, excludes all .git metadata, and is independent of the source Git HEAD. Check actual command exit codes; preserve diff and validation evidence as Artifacts. Previous shared-workspace observations do not establish facts about this copy."))
	}
	rows, err := tx.QueryContext(ctx, `SELECT instruction,artifact_refs FROM task_instruction WHERE task_id=? AND task_epoch<? ORDER BY sequence`, taskID, epoch)
	if err != nil {
		return err
	}
	for rows.Next() {
		var text, refs string
		if err = rows.Scan(&text, &refs); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, agent.RuntimeContext("task_instruction", "", false, struct {
			Instruction  string          `json:"instruction"`
			ArtifactRefs json.RawMessage `json:"artifact_refs"`
		}{text, json.RawMessage(refs)}))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT u.event_id,t.task_ref,u.kind,u.content,u.occurred_at FROM task_update u JOIN task t USING(task_id) WHERE u.task_id=? AND u.task_epoch<? ORDER BY u.task_epoch,u.occurred_at,u.event_id`, taskID, epoch)
	if err != nil {
		return err
	}
	for rows.Next() {
		var eventID, ref, kind, content string
		var at int64
		if err = rows.Scan(&eventID, &ref, &kind, &content, &at); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, taskMessage(eventID, ref, kind, content, nil, time.UnixMicro(at).UTC()))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Business continuity is scoped to Root-visible outputs. Never reconstruct
	// a Root by pooling Child generations or hidden Code Mode calls.
	handoff, err := r.taskHandoff(ctx, tx, taskID, epoch)
	if err != nil {
		return err
	}
	messages = append(messages, handoff...)
	var progress string
	if err = tx.QueryRowContext(ctx, `SELECT progress_summary FROM task WHERE task_id=?`, taskID).Scan(&progress); err != nil {
		return err
	}
	if progress != "" {
		messages = append(messages, agent.RuntimeContext("task_progress", "", false, progress))
	}

	cp, _ := encoded(agent.Checkpoint{ContextVersion: 2, Phase: "model", Messages: messages})
	if _, err = tx.ExecContext(ctx, `INSERT INTO run(run_id,agent_id,conversation_id,task_id,root_run_id,kind,worker_position,status,task_epoch,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES(?,?,?,?,?,'worker','root','queued',?,?,?,?,0,?,?,1)`, id, agentID, conversationID, taskID, id, epoch, capID, snapshot.SnapshotID, cp, now, now); err != nil {
		return err
	}
	if err = r.freezeContext(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO run_claim VALUES(?,0,NULL,0,1)`, id); err != nil {
		return err
	}
	if err = r.createTree(ctx, tx, id); err != nil {
		return err
	}
	return runEvent(ctx, tx, id, "run_ready", 1)
}

// executeTaskAdmission waits only for rolled-back Task admission. The shared
// transaction handles Task control only; append alone returns ErrBusy.
func (t *tools) executeTaskAdmission(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	for {
		result, err := t.executeTask(ctx, inv)
		if !errors.Is(err, agent.ErrBusy) {
			return result, err
		}
		t.runs.host.boundary("before_append_stop_wait")
		// An append racing a controlled stop must not create a second live
		// Root. Wait outside the transaction, then commit instruction/epoch/Root
		// together. This retries only a rolled-back admission, never execution.
		if err = t.runs.recoverStops(ctx); err != nil {
			return tool.Result{}, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return tool.Result{}, agent.ErrBusy
		case <-timer.C:
		}
	}
}

func (t *tools) executeTaskWithLabel(ctx context.Context, inv tool.Invocation, label taskLabel, feedback feedbackPolicy) (tool.Result, error) {
	result := tool.Failure("PERMISSION_DENIED")
	completion := false
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		completion = false
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if errors.Is(err, tool.ErrApprovalDenied) {
			return nil
		}
		if err != nil {
			return err
		}
		var source string
		if kind != "main" {
			return nil
		}
		if name == task.SearchName {
			request, err := task.ParseSearch(args)
			if err != nil {
				result = tool.Failure("INVALID_ARGUMENT")
				return nil
			}
			result, err = t.runs.legacySearchTasksV1(ctx, tx, t.claim.RunID(), request)
			return err
		}
		if name != task.ManageName {
			return nil
		}
		request, err := task.ParseManage(args)
		if err != nil {
			result = tool.Failure("INVALID_ARGUMENT")
			return nil
		}
		completion = request.Operation == "complete"
		scope, e := runScope(ctx, tx, t.claim.RunID())
		if e != nil {
			return e
		}
		for _, ref := range request.ArtifactRefs {
			if _, e = t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, ref, true); e != nil {
				result = artifactFailure(e)
				return nil
			}
		}
		canonical, _ := encoded(request)
		hash := digest(canonical)
		var oldHash, receipt string
		err = tx.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM task_invocation WHERE invocation_id=?`, inv.ID).Scan(&oldHash, &receipt)
		if err == nil {
			if oldHash != hash {
				return agent.ErrConflict
			}
			result = tool.Result{Text: receipt}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		// The source is the latest consumed input, never a late unconsumed Steering.
		if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT i.message_id FROM main_input i WHERE i.run_id=r.run_id AND i.sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY i.sequence DESC LIMIT 1),(SELECT request_id FROM main_inbox b WHERE b.run_id=r.run_id ORDER BY b.sequence DESC LIMIT 1),'') FROM run r WHERE r.run_id=?`, t.claim.RunID()).Scan(&source); err != nil {
			return err
		}
		if source == "" {
			err = tx.QueryRowContext(ctx, `SELECT request_id FROM github_task_followup WHERE run_id=? AND state IN('assigned','continued')`, t.claim.RunID()).Scan(&source)
			if err != nil {
				return agent.ErrConflict
			}
		}

		err = tx.QueryRowContext(ctx, `SELECT receipt_json FROM task_request WHERE source_message_id=? AND request_hash=?`, source, hash).Scan(&receipt)
		if err == sql.ErrNoRows {
			allowed, err := t.runs.checkGitHubAppend(ctx, tx, t.claim.RunID(), request)
			if err != nil {
				return err
			}
			if !allowed {
				result = tool.Failure("PERMISSION_DENIED")
				return nil
			}
			if request.Operation == "append" {
				if err := t.runs.guardFeedbackAppend(ctx, tx, source, request.TaskRef, request.FeedbackRef); err != nil {
					if errors.Is(err, task.ErrFeedbackStale) {
						result = tool.Failure("INVALID_ARGUMENT")
						return nil
					}
					return err
				}
			}
			var agentID, conv string
			if err = tx.QueryRowContext(ctx, `SELECT agent_id,conversation_id FROM run WHERE run_id=?`, t.claim.RunID()).Scan(&agentID, &conv); err != nil {
				return err
			}
			ref, taskID := request.TaskRef, ""
			receiptStatus := "active"
			epoch := int64(1)
			now := time.Now().UnixMicro()
			refs, _ := encoded(request.ArtifactRefs)
			change := taskChange{source: source, request: request, scope: scope, agentID: agentID, conversationID: conv, reference: ref, id: taskID, receiptStatus: receiptStatus, epoch: epoch, at: now, artifactsJSON: refs}
			if request.Operation == "create" {
				if label.Managed {
					if label.RequestHash != hash || !validTaskLabel(request, label.Title) {
						return agent.ErrConflict
					}
					if err = t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); errors.Is(err, rsi.ErrRevoked) {
						result = tool.Failure("PROVIDER_UNAVAILABLE")
						return nil
					} else if err != nil {
						return err
					}
					change.title = label.Title
				}
				if err = t.createManagedTask(ctx, tx, &change); err != nil {
					return err
				}
			} else {
				change.feedbackPolicy = feedback
				if feedback.managed && (request.Operation == "request_input" || request.Operation == "request_review") && feedback.requestHash != hash {
					return agent.ErrConflict
				}
				result, err = t.changeManagedTask(ctx, tx, &change)
				if err != nil || result.Error != nil {
					return err
				}
			}
			ref, taskID, receiptStatus, epoch = change.reference, change.id, change.receiptStatus, change.epoch
			if request.Operation == "append" {
				if err = acceptGitHubAppend(ctx, tx, t.claim.RunID(), taskID, epoch); err != nil {
					return err
				}
			}

			receipt, _ = encoded(task.Receipt{TaskRef: ref, Status: receiptStatus, Accepted: true, FeedbackRef: change.feedbackRef})
			if _, err = tx.ExecContext(ctx, `INSERT INTO task_request VALUES(?,?,?,?)`, source, hash, receipt, inv.ID); err != nil {
				return err
			}
			if err = runEvent(ctx, tx, taskID, "task."+request.Operation, epoch); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO task_invocation VALUES(?,?,?)`, inv.ID, hash, receipt); err != nil {
			return err
		}
		result = tool.Result{Text: receipt}
		if completion {
			t.runs.host.boundary("before_task_complete_commit")
		}
		t.runs.host.boundary("before_task_commit")
		return nil
	})
	if err == nil && result.Error == nil {
		if completion {
			t.runs.host.boundary("after_task_complete_commit")
		}
		t.runs.host.boundary("after_task_commit")
		var receipt task.Receipt
		if json.Unmarshal([]byte(result.Text), &receipt) == nil && (receipt.Status == "paused" || receipt.Status == "cancelled") {
			t.runs.host.boundary("after_stop_commit")
		}
	}
	return result, err
}

func (r *Runs) taskSteering(ctx context.Context, c agent.RunClaim, after int64) ([]agent.Input, error) {
	inputs := []agent.Input{}
	err := r.write(ctx, func(tx *sql.Tx) error {
		inputs = nil
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var frozen int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_generation g WHERE g.run_id=r.run_id AND g.step_index=json_extract(r.checkpoint_json,'$.step')) OR EXISTS(SELECT 1 FROM context_compaction c WHERE c.run_id=r.run_id AND c.step_index=json_extract(r.checkpoint_json,'$.step')) FROM run r WHERE r.run_id=?`, c.RunID()).Scan(&frozen); err != nil {
			return err
		}
		if frozen > 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.sequence,i.instruction,i.artifact_refs,coalesce(json_extract(p.policy_json,'$.input_recall'),0) FROM task_instruction i JOIN run r ON r.task_id=i.task_id AND r.task_epoch=i.task_epoch LEFT JOIN run_context_policy p USING(run_id) WHERE r.run_id=? AND r.worker_position='root' AND i.sequence>? ORDER BY i.sequence`, c.RunID(), after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i agent.Input
			var text, refs string
			var plainIDs bool
			if err = rows.Scan(&i.Sequence, &text, &refs, &plainIDs); err != nil {
				return err
			}
			label := "Artifact references: "
			if plainIDs {
				var ids []string
				if json.Unmarshal([]byte(refs), &ids) != nil {
					return agent.ErrStorage
				}
				refs, _ = encoded(tool.ObjectIDs(ids))
				label = "Artifact IDs: "
			}
			i.Message.Content = conversation.Text("Task instruction: " + text + "\n" + label + refs)
			inputs = append(inputs, i)
		}
		return rows.Err()
	})
	return inputs, err
}

func (r *Runs) prepareWorkerReply(ctx context.Context, c agent.RunClaim, run agent.Run) (bool, error) {
	ready := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_instruction i JOIN run r ON r.task_id=i.task_id AND r.task_epoch=i.task_epoch WHERE r.run_id=? AND r.worker_position='root' AND i.sequence>?`, run.ID, run.Checkpoint.InputSequence).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO worker_result VALUES(?,?,?,?) ON CONFLICT(exchange_id) DO NOTHING`, run.ID, run.Checkpoint.ExchangeID, run.Checkpoint.Result.Text, time.Now().UnixMicro()); err != nil {
			return err
		}
		ready = true
		return nil
	})
	return ready, err
}
func (r *Runs) deliverWorkerReply(ctx context.Context, c agent.RunClaim, run agent.Run) error {
	cp := run.Checkpoint
	cp.Step++
	cp.Phase = "settle"
	cp.Result = nil
	cp.ExchangeID = ""
	return r.Checkpoint(ctx, c, run.Version, cp)
}
