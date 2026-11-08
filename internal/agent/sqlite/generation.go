package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	deliverysqlite "github.com/z-chenhao/assistant/internal/delivery/sqlite"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func (r *Runs) Generation(ctx context.Context, c agent.RunClaim, run agent.Run) (agent.Generation, error) {
	owner, ok := c.(*claim)
	if !ok {
		return agent.Generation{}, agent.ErrClaimLost
	}
	var messages []llm.Message
	var compaction *agent.Compaction
	var legacy bool
	err := r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_context_generation l JOIN model_generation g USING(generation_id) WHERE g.run_id=? AND g.step_index=?)`, run.ID, run.Checkpoint.Step).Scan(&legacy)
	if err != nil {
		return agent.Generation{}, agent.ErrStorage
	}
	if run.Checkpoint.ContextVersion >= 2 && !legacy {
		prepared, e := r.prepareWorkingContext(ctx, c, run)
		if e != nil {
			return agent.Generation{}, e
		}
		messages, compaction = prepared.Messages, prepared.Compaction
	} else {
		messages, err = agent.ProviderMessages(run.Checkpoint.Messages, false, false)
		if err == nil {
			messages, err = r.recallMessages(ctx, c, run, messages, -1, false, false)
		}
		if err == nil {
			messages, err = r.compactMessages(ctx, c, run, messages)
		}
		if err != nil {
			return agent.Generation{}, err
		}
	}
	request := llm.GenerationRequest{RunID: run.ID, SessionID: run.ID, Snapshot: run.Snapshot, Messages: messages, OutputLimitBytes: 64 << 10}
	request.Tools = agent.ProviderTools(run.Tools, run.Checkpoint.LoadedTools)
	if compaction != nil {
		request.Tools = nil
		request.OutputLimitBytes = compaction.MaxBytes
	}
	// Compaction is threshold-based; this final bound rejects an irreducible turn.
	wire, _ := encoded(request.Messages)
	inputBytes := len(wire)
	if run.Checkpoint.ContextVersion >= 2 && !legacy {
		inputBytes = contextbuilder.ModelSize(request.Messages)
	}
	if int64(inputBytes) > run.Snapshot.ContextWindow*3 {
		return agent.Generation{}, llm.ContextLengthExceeded
	}
	var gen agent.Generation
	err = r.write(ctx, func(tx *sql.Tx) error {
		gen = agent.Generation{Compaction: compaction}
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		if run.Kind == "worker" {
			var limit int64
			if err := tx.QueryRowContext(ctx, `SELECT t.max_turns FROM run_tree t JOIN run r ON r.root_run_id=t.root_run_id WHERE r.run_id=?`, run.ID).Scan(&limit); err != nil {
				return err
			}
			if run.Checkpoint.Step >= limit {
				return agent.ErrResourceLimit
			}
		}
		var raw, hash, status string
		var result sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT generation_id,request_json,request_hash,status,result_json,error_code FROM model_generation WHERE run_id=? AND step_index=?`, run.ID, run.Checkpoint.Step).Scan(&request.GenerationID, &raw, &hash, &status, &result, &gen.ErrorCode)
		if err == sql.ErrNoRows {
			request.GenerationID = newID()
			raw, err = encoded(request)
			if err != nil {
				return err
			}
			hash = digest(raw)
			_, err = tx.ExecContext(ctx, `INSERT INTO model_generation(generation_id,run_id,step_index,request_json,request_hash,owner_token,status,created_at) VALUES(?,?,?,?,?,?,'pending',?)`, request.GenerationID, run.ID, run.Checkpoint.Step, raw, hash, owner.token, time.Now().UnixMicro())
			if err != nil {
				return err
			}
			if err = runEvent(ctx, tx, run.ID, "model_generation.accepted", run.Checkpoint.Step+1); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			expected, _ := encoded(request)
			if digest(expected) != hash {
				return agent.ErrConflict
			}
			if status == "completed" {
				var value llm.GenerationResult
				if json.Unmarshal([]byte(result.String), &value) != nil {
					return agent.ErrStorage
				}
				gen.Result = &value
			}
			if status == "pending" {
				if _, err = tx.ExecContext(ctx, `UPDATE model_generation SET owner_token=?,attempt_count=attempt_count+1 WHERE generation_id=?`, owner.token, request.GenerationID); err != nil {
					return err
				}
			}
		}
		gen.Request = request
		gen.Request.OwnerToken = owner.token
		if gen.Result == nil && gen.ErrorCode == "" {
			gen.Waiting, gen.Denied, err = r.gateSpend(ctx, tx, request)
			return err
		}
		return nil
	})
	if err == nil && gen.Result == nil && gen.ErrorCode == "" && !gen.Waiting && !gen.Denied {
		r.resolveGenerationImages(c, &gen.Request)
		for _, message := range gen.Request.Messages {
			if message.Memory != nil {
				gen.Request.ResolveMemory = r.memoryResolver(run.ID)
				break
			}
		}
		if run.Kind == "worker" {
			r.host.boundary("before_worker_generation")
		}
		r.host.boundary("before_generation_dispatch")
	}
	return gen, err
}

// AcceptGeneration runs inside the usage transaction, including auth epoch and
// provider cancellation finalization. Failed acceptance exposes no completion.
func (s *Store) AcceptGeneration(ctx context.Context, tx *sql.Tx, request llm.GenerationRequest, result llm.GenerationResult, cause error) error {
	if request.OwnerToken == "" {
		return nil
	} // control-plane capability Probe
	var token, runID, hash, status string
	if err := tx.QueryRowContext(ctx, `SELECT owner_token,run_id,request_hash,status FROM model_generation WHERE generation_id=?`, request.GenerationID).Scan(&token, &runID, &hash, &status); err != nil {
		return agent.ErrStorage
	}
	if token != request.OwnerToken || runID != request.RunID || status != "pending" {
		return agent.ErrClaimLost
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM run_claim WHERE run_id=? AND token=?`, runID, token).Scan(&epoch); err != nil {
		return agent.ErrClaimLost
	}
	if err := fence(ctx, tx, &claim{id: runID, token: token, epoch: epoch}); err != nil {
		return err
	}
	if request.ToolGeneration != nil {
		var name string
		if e := tx.QueryRowContext(ctx, `SELECT tool_name FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status='running'`, request.ToolGeneration.InvocationID, runID).Scan(&name); e != nil {
			return agent.ErrClaimLost
		}
		family, ok := tool.HostFamily(name)
		if !ok || family != tool.WebAccessBuiltin {
			return agent.ErrClaimLost
		}
		if cause == nil && (result.Clipped || len(result.ToolCalls) > 0) {
			return llm.InvalidProviderResponse
		}
	}
	if request.WebSearch != nil {
		var valid int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND run_id=? AND tool_name IN ('builtin__web_search','builtin__source_check') AND status='running'`, request.WebSearch.InvocationID, runID).Scan(&valid); err != nil {
			return agent.ErrStorage
		}
		if valid != 1 {
			return agent.ErrClaimLost
		}
		if cause == nil && (result.WebSearch == nil || result.Clipped || len(result.ToolCalls) != 0) {
			return llm.InvalidProviderResponse
		}
	}
	raw, _ := encoded(request)
	if hash != digest(raw) {
		return agent.ErrConflict
	}
	usage := result.Usage
	code := ""
	state := "completed"
	if cause != nil {
		code = llm.NormalizeError(cause.Error()).Error()
		result = llm.GenerationResult{}
		state = "failed"
	} else {
		if len(result.Text) > 64<<10 || len(result.ToolCalls) > 64 {
			return llm.InvalidProviderResponse
		}
		seen := map[string]bool{}
		for _, call := range result.ToolCalls {
			argumentLimit := 16 << 10
			if call.Name == codemode.ToolName || call.Name == ui.ShowName {
				// Both Code Mode and a complete visual answer carry bounded code.
				// Keep direct UI consistent with the catalog and bridge envelope.
				argumentLimit = 64 << 10
			}
			if call.ID == "" || len(call.ID) > 256 || call.Name == "" || seen[call.ID] || len(call.Arguments) > argumentLimit || !json.Valid([]byte(call.Arguments)) {
				return llm.InvalidProviderResponse
			}
			seen[call.ID] = true
		}
	}
	value, err := encoded(result)
	if err != nil {
		return agent.ErrStorage
	}
	s.boundary("before_generation_commit")
	if _, err = tx.ExecContext(ctx, `UPDATE model_generation SET status=?,result_json=?,error_code=?,completed_at=? WHERE generation_id=?`, state, value, code, time.Now().UnixMicro(), request.GenerationID); err != nil {
		return agent.ErrStorage
	}
	if err := runEvent(ctx, tx, runID, "model_generation."+state, 1); err != nil {
		return agent.ErrStorage
	}
	if err := s.settleSpend(ctx, tx, request, llm.GenerationResult{Usage: usage}); err != nil {
		return agent.ErrStorage
	}
	if err := s.estimateCost(ctx, tx, request, usage); err != nil {
		return agent.ErrStorage
	}
	if err := s.recordProviderHealth(ctx, tx, runID, code); err != nil {
		return agent.ErrStorage
	}
	return nil
}

func (r *Runs) PrepareReply(ctx context.Context, c agent.RunClaim, run agent.Run) (bool, error) {
	if run.Kind == "worker" {
		return r.prepareWorkerReply(ctx, c, run)
	}
	ready := false
	err := r.write(ctx, func(tx *sql.Tx) error {
		ready = false
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM main_reply WHERE exchange_id=?`, run.Checkpoint.ExchangeID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			ready = true
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM main_input WHERE run_id=? AND sequence>?`, run.ID, run.Checkpoint.InputSequence).Scan(&count); err != nil {
			return err
		}
		if count > 0 && !run.Checkpoint.SerialInputs {
			return nil
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM run WHERE run_id=?`, run.ID).Scan(&version); err != nil {
			return err
		}
		if version != run.Version {
			return agent.ErrConflict
		}
		messageID := "reply:" + run.Checkpoint.ExchangeID
		deliveryID := "delivery:" + messageID
		message := conversation.Message{MessageID: messageID, ConversationID: run.ConversationID, Role: "assistant", Content: conversation.Text(run.Checkpoint.Result.Text), Source: "main", SourceEventID: run.Checkpoint.ExchangeID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		if _, err := tx.ExecContext(ctx, `UPDATE ui_widget AS w SET message_id=? WHERE run_id=? AND message_id IS NULL AND invocation_id IN (SELECT invocation_id FROM tool_invocation WHERE status='succeeded') AND `+activeUIWidget, messageID, run.ID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT document_json FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE message_id=? ORDER BY `+uiSectionOrder, messageID)
		if err != nil {
			return err
		}
		var alternatives []conversation.ContentPart
		narrated := false
		for rows.Next() {
			var rawWidget string
			if err = rows.Scan(&rawWidget); err != nil {
				rows.Close()
				return err
			}
			widget, ok := ui.StoredWidget(rawWidget)
			if !ok {
				rows.Close()
				return agent.ErrStorage
			}
			narrated = narrated || widget.Text != ""
			alternatives = append(alternatives, conversation.ContentPart{Type: "text", Text: widget.Readable()})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if narrated {
			message.Content = append(alternatives, message.Content...)
		} else {
			message.Content = append(message.Content, alternatives...)
		}
		refs, err := published(ctx, tx, run.ID)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			// The selected resource was authorized by publish. Read only its immutable
			// semantic type here; media bytes never enter the Conversation intent.
			var media string
			if err = tx.QueryRowContext(ctx, `SELECT media_type FROM artifact WHERE artifact_ref=?`, ref).Scan(&media); err != nil {
				return err
			}
			message.Content = append(message.Content, conversation.ContentPart{Type: artifact.ContentType(media), ArtifactRef: ref})
		}
		raw, _ := encoded(message)
		if _, err := tx.ExecContext(ctx, `INSERT INTO main_reply VALUES(?,?,?,?)`, run.Checkpoint.ExchangeID, run.ID, messageID, deliveryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ui_action_request SET step_ceiling=?,reply_message_id=? WHERE message_id=(SELECT message_id FROM main_input WHERE run_id=? AND sequence=?)`, run.Checkpoint.Step, messageID, run.ID, run.Checkpoint.InputSequence); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_intent(message_id,message_json) VALUES(?,?)`, messageID, raw); err != nil {
			return err
		}
		if err := freezeMessageRoute(ctx, tx, messageID, run.ID); err != nil {
			return err
		}
		ready = true
		return runEvent(ctx, tx, run.ID, "main_reply.prepared", 1)
	})
	return ready, err
}

func (r *Runs) DeliverReply(ctx context.Context, c agent.RunClaim, run agent.Run) error {
	if run.Kind == "worker" {
		return r.deliverWorkerReply(ctx, c, run)
	}
	// Conversation must exist before DeliveryRequest, then Checkpoint. Each
	// interruption reuses the saved message/delivery IDs and fills missing work.
	if err := r.Flush(ctx); err != nil {
		return err
	}
	err := r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var deliveryID, messageID, raw string
		var flushed int
		if err := tx.QueryRowContext(ctx, `SELECT p.delivery_id,p.message_id,i.flushed,i.message_json FROM main_reply p JOIN conversation_intent i USING(message_id) WHERE exchange_id=? AND run_id=?`, run.Checkpoint.ExchangeID, run.ID).Scan(&deliveryID, &messageID, &flushed, &raw); err != nil {
			return err
		}
		if flushed != 1 {
			return agent.ErrConflict
		}
		var message conversation.Message
		if json.Unmarshal([]byte(raw), &message) != nil {
			return agent.ErrStorage
		}
		request := delivery.Request{DeliveryID: deliveryID, MessageID: messageID, ConversationID: run.ConversationID, Channel: "tui", DestinationRef: run.ConversationID, Intent: replyIntent(run), IdempotencyKey: deliveryID, CreatedAt: message.CreatedAt.UnixMicro()}
		if err := applyMessageRoute(ctx, tx, &request); err != nil {
			return err
		}
		if err := deliverysqlite.Enqueue(ctx, tx, request); err != nil {
			return err
		}
		r.host.boundary("before_delivery_commit")
		return nil
	})
	if err != nil {
		return err
	}
	r.host.boundary("after_delivery_commit")
	err = r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		cp := run.Checkpoint
		if cp.SerialInputs {
			cp.RepliedInputSequence = cp.InputSequence
		}
		cp.Step++
		cp.Phase = "settle"
		cp.ExchangeID = ""
		cp.Result = nil
		r.host.boundary("before_reply_checkpoint")
		return checkpoint(ctx, tx, c, run.Version, cp)
	})
	if err == nil {
		r.host.boundary("after_reply_checkpoint")
	}
	return err
}

func (r *Runs) Trace(ctx context.Context, c agent.RunClaim, e agent.Event) (agent.Event, error) {
	err := r.write(ctx, func(tx *sql.Tx) error {
		// agent_end is emitted after the terminal commit and no longer has a
		// lease. It is intentionally best-effort like other trace-only updates.
		if e.Kind != "agent_end" {
			if err := fence(ctx, tx, c); err != nil {
				return err
			}
		} else {
			owner, ok := c.(*claim)
			if !ok {
				return agent.ErrClaimLost
			}
			var valid int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM run r JOIN run_claim c USING(run_id) WHERE r.run_id=? AND c.epoch=? AND r.status IN ('completed','failed') AND r.desired_state='running' AND (r.kind='main' OR EXISTS(SELECT 1 FROM task t WHERE t.task_id=r.task_id AND t.task_epoch=r.task_epoch))`, owner.id, owner.epoch).Scan(&valid); err != nil {
				return err
			}
			if valid != 1 {
				return agent.ErrClaimLost
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM run_trace WHERE run_id=?`, c.RunID()).Scan(&e.Sequence); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT agent_id,kind,coalesce(task_id,'') FROM run WHERE run_id=?`, c.RunID()).Scan(&e.AgentID, &e.RunKind, &e.TaskID); err != nil {
			return err
		}
		e.RunID = c.RunID()
		raw, err := encoded(e)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO run_trace VALUES(?,?,?,?)`, c.RunID(), e.Sequence, raw, e.OccurredAt.UnixMicro())
		return err
	})
	if err == nil && e.Kind == "tool_execution_start" {
		// Explicit fault/barrier injection is outside the transaction, so a
		// held tool cannot monopolize SQLite and prevent concurrent ingress.
		r.host.boundary("after_tool_execution_start")
	}
	if err == nil && e.Kind == "turn_end" {
		// The Loop has committed the complete tool batch or reply checkpoint.
		r.host.boundary("after_turn_end")
	}
	return e, err
}

// Runtime follow-up semantics determine notification intent. Ordinary progress
// never enters Main context and cannot reach this composer boundary.
func replyIntent(run agent.Run) string {
	intent := "completion"
	for _, m := range run.Checkpoint.Messages {
		if m.Type != "custom" || m.Origin != "runtime" || m.Details == nil {
			continue
		}
		switch m.Details.Kind {
		case "needs_input", "unknown":
			return "needs_input"
		case "failed":
			intent = "failure"
		}
	}
	return intent
}
