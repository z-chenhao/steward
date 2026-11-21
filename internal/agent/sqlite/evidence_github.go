package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/observer/github"
)

// The same persisted causal association authorizes both directions. Earlier
// completed appends remain visible within the same live binding generation.
const githubEvidenceRows = ` FROM github_task_followup f
 JOIN github_task_binding b ON b.scope=f.scope AND b.generation=f.generation AND b.task_id=f.task_id
 JOIN github_watch w ON w.scope=b.scope
 JOIN github_observation o ON o.sequence=f.observation_sequence AND o.scope=f.scope
 JOIN task t ON t.task_id=f.task_id
 WHERE b.scope IN (SELECT value FROM json_each(?)) AND t.agent_id=? AND t.conversation_id=? AND b.privacy_epoch=?
 AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 AND t.created_at>? AND o.received_at>?
 AND b.enabled=1 AND b.rule_mode='automatic' AND w.enabled=1 AND w.repository_id IS NOT NULL
 AND w.failure_code NOT IN ('REPOSITORY_IDENTITY_CHANGED','CREDENTIAL_REJECTED')
 AND f.privacy_epoch=b.privacy_epoch AND w.intent_privacy_epoch=b.privacy_epoch AND o.intent_privacy_epoch=b.privacy_epoch
 AND (o.binding_generation=b.generation OR (o.binding_generation IS NULL AND b.rule_event_kind=''))
 AND b.task_epoch=t.task_epoch AND t.status IN ('active','completed') AND t.explicit_end_epoch<t.task_epoch
 AND ((f.state='assigned' AND f.task_epoch=t.task_epoch)
   OR (f.state='continued' AND f.continued_epoch>f.task_epoch AND f.continued_epoch<=t.task_epoch))`

func (a evidenceAccess) githubEvidenceArgs() []any {
	return []any{a.githubScopesJSON, a.scope.AgentID, a.conversation, a.epoch, a.floor, a.floor}
}

// ReadGitHubLinkedEvidence takes the owner's configured source without
// configuring a watcher or reconciling any bindings.
func (r *Runs) ReadGitHubLinkedEvidence(ctx context.Context, q evidence.Request, cfg *github.Config) (evidence.Page, error) {
	if cfg == nil {
		return r.ReadGitHubEvidenceForWatches(ctx, q, nil)
	}
	return r.ReadGitHubEvidenceForWatches(ctx, q, []github.Config{*cfg})
}
func (r *Runs) ReadGitHubEvidenceForWatches(ctx context.Context, q evidence.Request, watches []github.Config) (evidence.Page, error) {
	raw, err := encoded(q)
	if err != nil {
		return evidence.Page{}, evidence.ErrInvalid
	}
	if _, err = evidence.ParseOwner(raw); err != nil {
		return evidence.Page{}, err
	}
	if !strings.HasPrefix(q.Ref, "task:") && !strings.HasPrefix(q.Ref, "github:") {
		return evidence.Page{}, evidence.ErrInvalid
	}
	configured, err := gitHubScopesFor(watches)
	if err != nil {
		return evidence.Page{}, evidence.ErrInvalid
	}
	encoded := ""
	if len(configured.scopes) > 0 {
		encoded = configured.encoded
	}
	page, err := r.readScopedEvidence(ctx, q, nil, encoded)
	if err != nil {
		return evidence.Page{}, evidenceReadError(err)
	}
	return page, nil
}

func (r *Runs) readGitHubObservation(ctx context.Context, a evidenceAccess, q evidence.Request, page *evidence.Page) error {
	sequence, ok := evidence.GitHubSequence(q.Ref)
	if !ok || a.githubScopesJSON == "" {
		return artifact.ErrNotFound
	}
	var raw, taskRef string
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT o.content_json,t.task_ref`+githubEvidenceRows+` AND o.sequence=?`,
			append(a.githubEvidenceArgs(), sequence)...).Scan(&raw, &taskRef)
		if err == sql.ErrNoRows {
			return artifact.ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	var observation github.Observation
	if err = json.Unmarshal([]byte(raw), &observation); err != nil {
		return err
	}
	page.Kind = "github_observation"
	page.Title = observation.Kind
	if len(observation.Actor) <= 128 {
		page.Author = observation.Actor
	}
	page.OccurredAt = observation.UpdatedAt
	if q.LinksOffset == 0 {
		page.Links = append(page.Links, evidence.Link{Relation: "associated_task", Ref: taskRef})
	}
	text := observation.Body
	if observation.State != "" {
		text = "State: " + observation.State + "\n" + text
	}
	return r.evidenceWindow(ctx, a, page, text, q)
}

func (r *Runs) recheckGitHubEvidence(ctx context.Context, tx *sql.Tx, a evidenceAccess, page *evidence.Page) error {
	if a.githubScopesJSON == "" {
		return nil
	}
	visible := func(sequence int64, taskRef string) (bool, error) {
		var ok bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+githubEvidenceRows+` AND o.sequence=? AND (?='' OR t.task_ref=?))`,
			append(a.githubEvidenceArgs(), sequence, taskRef, taskRef)...).Scan(&ok)
		return ok, err
	}
	if sequence, ok := evidence.GitHubSequence(page.Ref); ok {
		allowed, err := visible(sequence, "")
		if err == nil && !allowed {
			return artifact.ErrNotFound
		}
		return err
	}
	links := page.Links[:0]
	for _, link := range page.Links {
		if sequence, ok := evidence.GitHubSequence(link.Ref); ok {
			allowed, err := visible(sequence, page.Ref)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
		}
		links = append(links, link)
	}
	page.Links = links
	return nil
}
