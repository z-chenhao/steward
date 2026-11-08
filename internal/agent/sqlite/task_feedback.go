package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (t *tools) requestTaskFeedback(ctx context.Context, tx *sql.Tx, change *taskChange) (tool.Result, error) {
	if change.status != "active" && change.status != "completed" {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	var epoch int64
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(p.epoch,0),coalesce(t.created_at<=p.updated_at,0) FROM task t LEFT JOIN memory_privacy p USING(agent_id) WHERE t.task_id=?`, change.id).Scan(&epoch, &revoked); err != nil {
		return tool.Result{}, err
	}
	if revoked {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_feedback SET status='superseded' WHERE task_id=? AND status='pending'`, change.id); err != nil {
		return tool.Result{}, err
	}
	change.feedbackRef = "feedback:" + newID()
	kind := "input"
	if change.request.Operation == "request_review" {
		kind = "review"
	}
	// A Reporter source is a runtime event, not a user message. Preserve the
	// actual original user message when available instead of labelling an event
	// ID as a Conversation reference.
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT c.message_id FROM conversation_intent c WHERE c.message_id=?),(SELECT c.message_id FROM task_request q JOIN conversation_intent c ON c.message_id=q.source_message_id WHERE json_extract(q.receipt_json,'$.taskRef')=? ORDER BY c.sequence LIMIT 1),'')`, change.source, change.reference).Scan(&source); err != nil {
		return tool.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_feedback(feedback_ref,task_id,task_epoch,privacy_epoch,kind,prompt,source_message_id,artifact_refs,status,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,'pending',?,?)`, change.feedbackRef, change.id, change.epoch, epoch, kind, change.request.Prompt, source, change.artifactsJSON, change.at, change.at+int64(7*24*time.Hour/time.Microsecond)); err != nil {
		return tool.Result{}, err
	}
	for _, ref := range change.request.ArtifactRefs {
		if err := t.runs.host.Artifacts().PinTx(ctx, tx, change.scope, ref, "task", change.id); err != nil {
			return tool.Result{}, err
		}
	}
	if err := t.runs.saveFeishuTaskFeedbackCard(ctx, tx, change, kind, source); err != nil {
		return tool.Result{}, err
	}
	change.receiptStatus = change.status
	return tool.Result{}, nil
}

const feedbackColumns = `f.feedback_ref,t.task_ref,t.title,f.kind,f.prompt,f.source_message_id,coalesce((SELECT sequence FROM conversation_intent WHERE message_id=f.source_message_id),0),f.artifact_refs,
 CASE WHEN f.status='pending' AND (f.expires_at<=? OR f.task_epoch!=t.task_epoch OR t.status NOT IN ('active','completed') OR f.privacy_epoch!=coalesce(p.epoch,0) OR t.created_at<=coalesce(p.updated_at,0)) THEN 'expired' ELSE f.status END,
 f.created_at,f.expires_at`

func scanFeedback(row interface{ Scan(...any) error }) (task.Feedback, error) {
	var out task.Feedback
	var refs string
	var created, expires int64
	err := row.Scan(&out.Ref, &out.TaskRef, &out.TaskTitle, &out.Kind, &out.Prompt, &out.SourceMessageID, &out.SourceSequence, &refs, &out.Status, &created, &expires)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(refs), &out.ArtifactRefs); err != nil {
		return out, err
	}
	out.CreatedAt = time.UnixMicro(created).UTC().Format(time.RFC3339Nano)
	out.ExpiresAt = time.UnixMicro(expires).UTC().Format(time.RFC3339Nano)
	return out, nil
}

// TaskFeedbacks is an owner-only read of current pending requests. Privacy
// revocation excludes old question content; reads never mutate lifecycle.
func (s *Store) TaskFeedbacks(ctx context.Context, taskRef, before string) (task.FeedbackPage, error) {
	out := task.FeedbackPage{Items: []task.Feedback{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, agent.ErrStorage
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+feedbackColumns+` FROM task_feedback f JOIN task t USING(task_id) LEFT JOIN memory_privacy p USING(agent_id)
 WHERE t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 AND (?='' OR t.task_ref=?) AND (?='' OR f.rowid<(SELECT b.rowid FROM task_feedback b JOIN task bt USING(task_id) WHERE b.feedback_ref=? AND bt.agent_id=t.agent_id))
 AND f.status='pending' AND f.expires_at>? AND f.task_epoch=t.task_epoch AND t.status IN ('active','completed')
 AND f.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0)
 ORDER BY f.rowid DESC LIMIT 51`, time.Now().UnixMicro(), taskRef, taskRef, before, before, time.Now().UnixMicro())
	if err != nil {
		return out, agent.ErrStorage
	}
	candidates := []task.Feedback{}
	for rows.Next() {
		v, err := scanFeedback(rows)
		if err != nil {
			rows.Close()
			return out, agent.ErrStorage
		}
		if len(candidates) == 50 {
			out.Next = candidates[49].Ref
			break
		}
		candidates = append(candidates, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, agent.ErrStorage
	}
	// Reuse ArtifactStore's owner/access/retention predicates in the same read
	// snapshot. Do not duplicate its task-protected expiry rules or open blobs.
	// A page inspects at most 50 candidates; Next also advances past hidden
	// stale artifacts, so filtering cannot strand older current requests.
	for _, v := range candidates {
		if err := s.requireCurrentFeedback(ctx, tx, v.Ref, v.TaskRef, false); err != nil {
			if errors.Is(err, task.ErrFeedbackStale) {
				continue
			}
			return out, agent.ErrStorage
		}
		out.Items = append(out.Items, v)
	}
	if err := tx.Commit(); err != nil {
		return out, agent.ErrStorage
	}
	return out, nil
}

// RespondTaskFeedback only accepts owner input to Main. The existing Main
// submit transaction validates and consumes the request atomically with its
// conversation intent. It never appends work or changes an Approval.
func (r *Runs) RespondTaskFeedback(ctx context.Context, ref, answer string) (agent.Receipt, error) {
	return r.respondTaskFeedback(ctx, ref, answer, nil)
}

// RespondTaskFeedbackInbound accepts a bound Web or Feishu response through the same
// feedback identity as the owner CLI. The first accepted source keeps its route.
func (r *Runs) RespondTaskFeedbackInbound(ctx context.Context, e interaction.InboundEvent, ref, answer string) (agent.Receipt, error) {
	if e.Validate() != nil {
		return agent.Receipt{}, interaction.ErrUnauthorized
	}
	return r.respondTaskFeedback(ctx, ref, answer, &e)
}

func (r *Runs) respondTaskFeedback(ctx context.Context, ref, answer string, inbound *interaction.InboundEvent) (agent.Receipt, error) {
	if !task.ValidFeedbackAnswer(answer) {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	f, err := scanFeedback(r.host.db.QueryRowContext(ctx, `SELECT `+feedbackColumns+` FROM task_feedback f JOIN task t USING(task_id) LEFT JOIN memory_privacy p USING(agent_id) WHERE feedback_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, time.Now().UnixMicro(), ref))
	if err == sql.ErrNoRows {
		return agent.Receipt{}, task.ErrNotFound
	}
	if err != nil {
		return agent.Receipt{}, agent.ErrStorage
	}
	data, err := encoded(struct {
		FeedbackRef string   `json:"feedback_ref"`
		TaskRef     string   `json:"task_ref"`
		Kind        string   `json:"kind"`
		Prompt      string   `json:"prompt"`
		Artifacts   []string `json:"artifact_refs"`
		Answer      string   `json:"answer"`
	}{f.Ref, f.TaskRef, f.Kind, f.Prompt, f.ArtifactRefs, answer})
	if err != nil {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	text := "Response to a saved Task question or artifact review. If continuing through task_manage append, copy feedback_ref to feedbackRef, pass artifact_refs as artifactRefs, and use the exact Task reference; this response grants no external-action approval.\n" + data
	// Keep valid maximum-size text usable even when JSON escaping expands it
	// beyond one ContentPart. Provider projection concatenates adjacent text.
	parts := []conversation.ContentPart{}
	for len(text) > 0 {
		n := min(len(text), 64<<10)
		for n < len(text) && !utf8.RuneStart(text[n]) {
			n--
		}
		parts = append(parts, conversation.ContentPart{Type: "text", Text: text[:n]})
		text = text[n:]
	}
	for _, artifactRef := range f.ArtifactRefs {
		parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: artifactRef})
	}
	if inbound != nil {
		// The question is the logical source; page submissions are callback
		// aliases. This preserves cross-page retries without extra source rows.
		if inbound.Channel == "web" {
			inbound.SourceID = "task-feedback:" + ref
		}
		inbound.Content = parts
	}
	return r.submit(ctx, "task-feedback:"+ref, parts, inbound, ref, nil)
}

// requireCurrentFeedback joins ingress and later Main Task admission so a
// pause/cancel/Forget/new epoch after an answer cannot be undone by that answer.
func (s *Store) requireCurrentFeedback(ctx context.Context, tx *sql.Tx, ref, taskRef string, answered bool) error {
	status := "pending"
	if answered {
		status = "answered"
	}
	var refs, agentID string
	err := tx.QueryRowContext(ctx, `SELECT f.artifact_refs,t.agent_id FROM task_feedback f JOIN task t USING(task_id) LEFT JOIN memory_privacy p USING(agent_id) WHERE f.feedback_ref=? AND f.status=? AND (?='' OR t.task_ref=?) AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status IN ('active','completed') AND t.task_epoch=f.task_epoch AND f.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0) AND f.expires_at>? AND NOT EXISTS(SELECT 1 FROM task_feedback newer WHERE newer.task_id=f.task_id AND newer.rowid>f.rowid)`, ref, status, taskRef, taskRef, time.Now().UnixMicro()).Scan(&refs, &agentID)
	if err == sql.ErrNoRows {
		return task.ErrFeedbackStale
	}
	if err != nil {
		return err
	}
	var artifacts []string
	if err := json.Unmarshal([]byte(refs), &artifacts); err != nil {
		return err
	}
	// Artifact refs are immutable revisions; a new review supersedes the old
	// request, and deletion/expiry must not turn into a review of missing data.
	for _, ref := range artifacts {
		if _, err := s.Artifacts().DescribeTx(ctx, tx, artifact.Scope{UserID: s.OwnerID(), AgentID: agentID, Owner: true}, ref, true); err != nil {
			if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrIntegrity) {
				return task.ErrFeedbackStale
			}
			return err
		}
	}
	return nil
}

func (r *Runs) guardFeedbackAppend(ctx context.Context, tx *sql.Tx, source, taskRef, expectedRef string) error {
	var ref string
	err := tx.QueryRowContext(ctx, `SELECT feedback_ref FROM task_feedback WHERE response_message_id=?`, source).Scan(&ref)
	if err == sql.ErrNoRows {
		if expectedRef != "" {
			return r.host.requireCurrentFeedback(ctx, tx, expectedRef, taskRef, true)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if expectedRef != "" && expectedRef != ref {
		return task.ErrFeedbackStale
	}
	return r.host.requireCurrentFeedback(ctx, tx, ref, taskRef, true)
}
