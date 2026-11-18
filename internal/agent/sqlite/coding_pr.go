package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/observer/github"
)

type codePRHandoff struct {
	source   string
	cfg      github.Config
	expected string
	binding  codePRBinding
}

type codePRBinding struct {
	observation                                       coding.PRAssociation
	scope, taskID                                     string
	watchVersion, generation, taskEpoch, privacyEpoch int64
}

// SaveCodePRReport reads the persisted observer state, then uses the existing
// report and Artifact commit path. The configuration is trusted owner input.
func (s *Store) SaveCodePRReport(ctx context.Context, ref string, cfg github.Config, expected string) (artifact.Descriptor, error) {
	return s.saveCodePRReport(ctx, ref, cfg, expected, "")
}

// SaveCodePRExactReport requires the observed commit's Git objects to exist in
// the task's configured source repository.
func (s *Store) SaveCodePRExactReport(ctx context.Context, ref string, cfg github.Config, expected, source string) (artifact.Descriptor, error) {
	if source == "" {
		return artifact.Descriptor{}, coding.ErrInvalid
	}
	return s.saveCodePRReport(ctx, ref, cfg, expected, source)
}

func (s *Store) saveCodePRReport(ctx context.Context, ref string, cfg github.Config, expected, source string) (artifact.Descriptor, error) {
	if cfg.Validate() != nil || !github.ValidHeadSHA(expected) {
		return artifact.Descriptor{}, coding.ErrInvalid
	}
	expected = strings.ToLower(expected)
	binding, err := readCodePRBinding(ctx, s.db.QueryRowContext, ref, cfg, expected)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	if source != "" {
		var originalSource string
		if err = s.db.QueryRowContext(ctx, `SELECT source_path FROM task_code_environment WHERE task_id=?`, binding.taskID).Scan(&originalSource); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return artifact.Descriptor{}, agent.ErrNotFound
			}
			return artifact.Descriptor{}, agent.ErrStorage
		}
		if source != originalSource {
			return artifact.Descriptor{}, agent.ErrConflict
		}
	}
	return s.saveCodeReport(ctx, ref, &codePRHandoff{cfg: cfg, expected: expected, binding: binding, source: source})
}

func readCodePRBinding(ctx context.Context, query func(context.Context, string, ...any) *sql.Row, ref string, cfg github.Config, expected string) (codePRBinding, error) {
	var b codePRBinding
	var observed int64
	p := &b.observation
	err := query(ctx, `SELECT w.repository,w.repository_id,w.pull_number,w.head_sha,w.last_success_at,w.version,b.scope,b.generation,t.task_id,t.task_epoch,b.privacy_epoch
 FROM github_watch w JOIN github_task_binding b USING(scope) JOIN task t USING(task_id)
 WHERE w.scope=? AND lower(w.repository)=lower(?) AND w.pull_number=? AND lower(w.head_sha)=?
 AND w.repository_id>0 AND w.enabled=1 AND w.state='healthy' AND w.failure_code='' AND w.last_success_at IS NOT NULL
 AND b.enabled=1 AND b.task_epoch=t.task_epoch AND b.privacy_epoch=w.intent_privacy_epoch AND b.privacy_epoch=`+githubEpoch+`
 AND t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.conversation_id=t.agent_id
 AND t.status IN ('active','completed') AND t.explicit_end_epoch<t.task_epoch
 AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0)`,
		cfg.Scope(), cfg.Repository, cfg.PullNumber, expected, ref).Scan(&p.Repository, &p.RepositoryID, &p.PullNumber, &p.HeadSHA, &observed, &b.watchVersion, &b.scope, &b.generation, &b.taskID, &b.taskEpoch, &b.privacyEpoch)
	if err == sql.ErrNoRows {
		return codePRBinding{}, agent.ErrConflict
	}
	if err != nil {
		return codePRBinding{}, agent.ErrStorage
	}
	p.ObservedAt = time.UnixMicro(observed).UTC()
	if time.Since(p.ObservedAt) > 2*cfg.Interval() {
		return codePRBinding{}, agent.ErrConflict
	}
	p.Kind = "owner_requested_association"
	return b, nil
}
