package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
)

// Links come only from accepted typed references. Textual mentions, similarity,
// Worker traces and arbitrary Artifact contents never establish relationships.
func (r *Runs) evidenceLinks(ctx context.Context, a evidenceAccess, q evidence.Request, parts []conversation.ContentPart, page *evidence.Page) error {
	var query string
	var args []any
	switch page.Kind {
	case "conversation":
		raw, _ := json.Marshal(parts)
		id := strings.TrimPrefix(q.Ref, "conversation:")
		query = `SELECT json_extract(j.value,'$.artifact_ref') ref,'attachment' relation FROM json_each(?) j WHERE json_extract(j.value,'$.artifact_ref') IS NOT NULL
 UNION SELECT t.task_ref,'associated_task' FROM task_request q JOIN task t ON t.task_ref=json_extract(q.receipt_json,'$.taskRef') WHERE q.source_message_id=? AND t.agent_id=?
 UNION SELECT t.task_ref,'associated_task' FROM main_reply m JOIN main_inbox b ON b.run_id=m.run_id JOIN task_update u USING(event_id) JOIN task t USING(task_id) WHERE m.message_id=? AND t.agent_id=?`
		args = []any{string(raw), id, a.scope.AgentID, id, a.scope.AgentID}
	case "task":
		query = `SELECT j.value ref,'attachment' relation FROM task t,json_each(t.anchor_artifact_refs) j WHERE t.task_ref=?
 UNION SELECT j.value,'attachment' FROM task_instruction i JOIN task t USING(task_id),json_each(i.artifact_refs) j WHERE t.task_ref=?
 UNION SELECT j.value,'attachment' FROM task_update u JOIN task t USING(task_id),json_each(u.artifact_refs) j WHERE t.task_ref=?
 UNION SELECT 'conversation:'||q.source_message_id,'original_message' FROM task_request q WHERE json_extract(q.receipt_json,'$.taskRef')=? AND q.source_message_id!=''
 UNION SELECT 'conversation:'||m.message_id,'delivery' FROM task_update u JOIN task t USING(task_id) JOIN main_inbox b USING(event_id) JOIN main_reply m ON m.run_id=b.run_id WHERE t.task_ref=?`
		args = []any{q.Ref, q.Ref, q.Ref, q.Ref, q.Ref}
		if a.githubScopesJSON != "" {
			query += ` UNION SELECT 'github:'||o.sequence,'github_observation'` + githubEvidenceRows + ` AND t.task_ref=?`
			args = append(args, a.githubEvidenceArgs()...)
			args = append(args, q.Ref)
		}
	case "artifact":
		query = `SELECT t.task_ref ref,'associated_task' relation FROM task t WHERE t.agent_id=? AND (t.task_id=(SELECT task_id FROM artifact WHERE artifact_ref=?) OR EXISTS(SELECT 1 FROM artifact_reference ar WHERE ar.artifact_ref=? AND ar.holder_kind='task' AND ar.holder_id=t.task_id))`
		args = []any{a.scope.AgentID, q.Ref, q.Ref}
	}
	if query == "" {
		return nil
	}
	var candidates []evidence.Link
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		args = append(args, q.Ref, q.LinksOffset)
		rows, err := tx.QueryContext(ctx, `SELECT ref,relation FROM (`+query+`) WHERE ref!=? ORDER BY ref,relation LIMIT 21 OFFSET ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var link evidence.Link
			if err = rows.Scan(&link.Ref, &link.Relation); err != nil {
				return err
			}
			candidates = append(candidates, link)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	take, next, err := r.evidenceLinkWindow(ctx, a, q, len(candidates))
	if err != nil {
		return err
	}
	page.NextLinksOffset = next
	candidates = candidates[:take]
	for _, link := range candidates {
		if a.githubScopesJSON != "" && link.Relation == "github_observation" {
			// The final state snapshot rechecks these associations together.
			page.Links = append(page.Links, link)
			continue
		}
		if !evidence.ValidRef(link.Ref) {
			continue
		}
		visible, err := r.evidenceLinkVisible(ctx, a, link.Ref)
		if err != nil {
			return err
		}
		if visible {
			page.Links = append(page.Links, link)
		}
	}
	return nil
}

func (r *Runs) evidenceLinkVisible(ctx context.Context, a evidenceAccess, ref string) (bool, error) {
	if strings.HasPrefix(ref, "conversation:") {
		_, err := r.evidenceMessage(ctx, a, strings.TrimPrefix(ref, "conversation:"))
		if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, conversation.ErrConflict) {
			return false, nil
		}
		return err == nil, err
	}
	visible := false
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		if strings.HasPrefix(ref, "task:") {
			return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task WHERE task_ref=? AND agent_id=? AND conversation_id=? AND created_at>?)`, ref, a.scope.AgentID, a.conversation, a.floor).Scan(&visible)
		}
		if strings.HasPrefix(ref, "artifact:") {
			d, err := r.host.Artifacts().DescribeTx(ctx, tx, a.scope, ref, true)
			if errors.Is(err, artifact.ErrNotFound) {
				return nil
			}
			visible = err == nil && d.CreatedAt.UnixMicro() > a.floor
			return err
		}
		return nil
	})
	return visible, err
}
