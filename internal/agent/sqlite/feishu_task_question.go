package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/task"
)

// ConfigureFeishuReviewWorkspace supplies the validated owner configuration
// before the runtime accepts work.
func (r *Runs) ConfigureFeishuReviewWorkspace(workspace string) {
	r.feishuReviewWorkspace = workspace
}

// Only feedback derived from an owner-authored Feishu input can reserve an
// interactive notice. The route and binding version commit with the request.
func (r *Runs) saveFeishuTaskFeedbackCard(ctx context.Context, tx *sql.Tx, change *taskChange, kind, source string) error {
	reviewURL := ""
	if kind == "review" {
		if r.feishuReviewWorkspace == "" {
			return nil
		}
		reviewURL = strings.TrimRight(r.feishuReviewWorkspace, "/") + "/tasks/" + url.PathEscape(change.reference) + "#" + url.PathEscape(change.feedbackRef)
	}
	var binding, remote, title string
	var version int64
	err := tx.QueryRowContext(ctx, `SELECT b.binding_id,m.reply_source_id,b.version,t.title
 FROM main_input i JOIN message_route m USING(message_id) JOIN channel_binding b USING(binding_id)
 JOIN channel_preference p ON p.singleton=1 JOIN task t ON t.task_id=?
 WHERE i.message_id=? AND i.agent_id=t.agent_id AND b.channel='feishu' AND b.enabled=1 AND m.reply_source_id!=''
 AND json_extract(i.message_json,'$.role')='user' AND json_extract(i.message_json,'$.source')='feishu'
 AND json_extract(i.message_json,'$.source_event_id')=m.reply_source_id
 AND (p.channel='auto' OR (p.channel='feishu' AND p.binding_id=b.binding_id))`, change.id, source).Scan(&binding, &remote, &version, &title)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	id := "feishu-question:" + change.feedbackRef
	text := "Question about " + title + "\n\n" + change.request.Prompt
	if kind == "review" {
		id = "feishu-review:" + change.feedbackRef
		text = "Review this Task result in Steward, then send your feedback here."
	}
	message := conversation.Message{MessageID: id, ConversationID: change.conversationID, Role: "assistant",
		Content: conversation.Text(text),
		Source:  "task_feedback", SourceEventID: id, CreatedAt: time.UnixMicro(change.at).UTC()}
	raw, err := encoded(message)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conversation_intent(message_id,message_json) VALUES(?,?)`, id, raw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO message_route(message_id,binding_id,reply_source_id) VALUES(?,?,?)`, id, binding, remote); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_intent VALUES(?,'needs_input')`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO feishu_task_question(feedback_ref,message_id,binding_version,review_url) VALUES(?,?,?,?)`, change.feedbackRef, id, version, reviewURL)
	return err
}

// An answered question may replay its accepted receipt after the normal Task
// continuation advances the epoch. A new answer always needs the original epoch.
const feishuFeedbackCurrent = `((f.kind='input' AND q.review_url='') OR (f.kind='review' AND q.review_url!=''))
 AND f.status IN ('pending','answered')
 AND (f.task_epoch=t.task_epoch OR (f.status='answered' AND EXISTS(
  SELECT 1 FROM task_instruction i WHERE i.task_id=t.task_id AND i.task_epoch=t.task_epoch AND i.source_message_id=f.response_message_id
 )))
 AND t.status IN ('active','completed') AND t.explicit_end_epoch<t.task_epoch
 AND f.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0) AND f.expires_at>?
 AND NOT EXISTS(SELECT 1 FROM task_feedback newer WHERE newer.task_id=f.task_id AND newer.rowid>f.rowid)`

// FeishuTaskFeedbackCard reads the frozen presentation and live eligibility in
// one snapshot. Receipt queries can still settle an inactive card.
func (s *Store) FeishuTaskFeedbackCard(ctx context.Context, messageID string) (ref, reviewURL string, pending bool, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", "", false, err
	}
	defer tx.Rollback()
	var refs, agentID string
	err = tx.QueryRowContext(ctx, `SELECT q.feedback_ref,q.review_url,f.artifact_refs,t.agent_id,
 f.status='pending' AND b.enabled=1 AND b.version=q.binding_version AND (`+feishuFeedbackCurrent+`)
 FROM feishu_task_question q JOIN task_feedback f USING(feedback_ref) JOIN task t USING(task_id)
 LEFT JOIN memory_privacy p USING(agent_id) JOIN message_route m USING(message_id) JOIN channel_binding b USING(binding_id)
 WHERE q.message_id=?`, time.Now().UnixMicro(), messageID).Scan(&ref, &reviewURL, &refs, &agentID, &pending)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err == nil && pending && reviewURL != "" {
		err = s.requireFeishuReviewArtifacts(ctx, tx, refs, agentID)
		if errors.Is(err, task.ErrFeedbackStale) {
			pending, err = false, nil
		}
	}
	return ref, reviewURL, pending, err
}

// Authorize from the exact remote card receipt, then verify review revisions
// in the same transaction as callback dedup, replay and Main acceptance.
func (s *Store) requireFeishuTaskFeedbackCard(ctx context.Context, tx *sql.Tx, e interaction.InboundEvent, ref string) error {
	var current bool
	var kind, refs, agentID string
	err := tx.QueryRowContext(ctx, `SELECT (`+feishuFeedbackCurrent+`),f.kind,f.artifact_refs,t.agent_id
 FROM feishu_task_question q JOIN task_feedback f USING(feedback_ref) JOIN task t USING(task_id)
 LEFT JOIN memory_privacy p USING(agent_id) JOIN message_route m USING(message_id) JOIN channel_binding b USING(binding_id)
 JOIN delivery_request d ON d.message_id=q.message_id JOIN channel_receipt c ON c.delivery_id=d.delivery_id AND c.part=0
 WHERE q.feedback_ref=? AND c.remote_id=? AND d.channel='feishu' AND d.intent='needs_input' AND d.destination_ref=b.destination
 AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND d.conversation_id=t.conversation_id
 AND b.channel='feishu' AND b.enabled=1 AND b.version=q.binding_version AND b.subject=? AND b.destination=?`,
		time.Now().UnixMicro(), ref, e.SourceID, e.Subject, e.Destination).Scan(&current, &kind, &refs, &agentID)
	if err == sql.ErrNoRows {
		return interaction.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if !current {
		return task.ErrFeedbackStale
	}
	if kind == "review" {
		return s.requireFeishuReviewArtifacts(ctx, tx, refs, agentID)
	}
	return nil
}

func (s *Store) requireFeishuReviewArtifacts(ctx context.Context, tx *sql.Tx, raw, agentID string) error {
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return err
	}
	if len(refs) == 0 {
		return task.ErrFeedbackStale
	}
	scope := artifact.Scope{UserID: s.OwnerID(), AgentID: agentID, Owner: true}
	for _, ref := range refs {
		file, err := s.Artifacts().OpenFileTx(ctx, tx, scope, ref)
		if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrIntegrity) {
			return task.ErrFeedbackStale
		}
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}
