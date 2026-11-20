package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/task"
)

// This predicate only applies to Main Runs created for a GitHub observation.
// Ordinary Main/Worker claims retain exactly their existing fence semantics.
const currentGitHubCause = `NOT EXISTS(SELECT 1 FROM github_task_followup f WHERE f.run_id=r.run_id AND NOT EXISTS(
 SELECT 1 FROM github_task_binding b JOIN github_watch w USING(scope) JOIN task t ON t.task_id=b.task_id JOIN github_observation o ON o.sequence=f.observation_sequence
 WHERE b.scope=f.scope AND b.generation=f.generation AND b.enabled=1 AND b.rule_mode='automatic' AND w.enabled=1
 AND f.state IN('assigned','continued') AND b.task_id=f.task_id AND b.task_epoch=t.task_epoch
 AND t.agent_id=r.agent_id AND t.conversation_id=r.conversation_id AND t.task_epoch=coalesce(f.continued_epoch,f.task_epoch) AND t.status IN('active','completed') AND t.explicit_end_epoch<t.task_epoch
 AND (` + validGitHubSourceMethod + `) AND b.privacy_epoch=f.privacy_epoch AND b.privacy_epoch=` + githubEpoch + ` AND w.intent_privacy_epoch=b.privacy_epoch
 AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0)))`

// Selection, active waiting, and claiming use the same local eligibility.
// A retired process must not spin on another scope's queued Main.
const configuredGitHubRun = `(r.kind!='main' OR NOT EXISTS(SELECT 1 FROM github_task_followup f WHERE f.run_id=r.run_id AND f.scope NOT IN (SELECT value FROM json_each(:github_scopes))))`

func (r *Runs) githubScopeArgument() sql.NamedArg {
	return sql.Named("github_scopes", r.host.currentGitHubScopes().encoded)
}

// Each process uses only its configured PRs. Startup also revokes bindings
// removed from configuration, keeping other scopes and generations intact.
type gitHubScopeSet struct {
	scopes  []string
	encoded string
}

func gitHubScopesFor(watches []github.Config) (gitHubScopeSet, error) {
	scopes, err := github.ConfiguredScopes(watches)
	if err != nil {
		return gitHubScopeSet{}, err
	}
	encoded, err := json.Marshal(scopes)
	return gitHubScopeSet{scopes, string(encoded)}, err
}
func (s *Store) ConfigureGitHub(ctx context.Context, cfg *github.Config) error {
	if cfg == nil {
		return s.ConfigureGitHubWatches(ctx, nil)
	}
	return s.ConfigureGitHubWatches(ctx, []github.Config{*cfg})
}
func (s *Store) ConfigureGitHubWatches(ctx context.Context, watches []github.Config) error {
	configured, err := gitHubScopesFor(watches)
	if err != nil {
		return err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error { return revokeOtherGitHubScopes(ctx, tx, configured.encoded) })
	if err == nil {
		s.githubScopes.Store(&configured)
	}
	return githubStorageError(err)
}
func (s *Store) currentGitHubScopes() gitHubScopeSet {
	if configured := s.githubScopes.Load(); configured != nil {
		return *configured
	}
	return gitHubScopeSet{encoded: "[]"}
}
func revokeOtherGitHubScopes(ctx context.Context, tx *sql.Tx, allowed string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE github_task_binding SET enabled=0,generation=generation+1,version=version+1 WHERE enabled=1 AND scope NOT IN (SELECT value FROM json_each(?))`, allowed); err != nil {
		return err
	}
	return stopInvalidGitHubMains(ctx, tx)
}

func (s *Store) BindGitHubTask(ctx context.Context, cfg github.Config, q github.BindingRequest, enabled bool) error {
	return s.BindGitHubTaskIn(ctx, cfg, []github.Config{cfg}, q, enabled)
}
func (s *Store) BindGitHubTaskIn(ctx context.Context, cfg github.Config, watches []github.Config, q github.BindingRequest, enabled bool) error {
	configured, err := gitHubScopesFor(watches)
	if err != nil || !slices.Contains(configured.scopes, cfg.Scope()) {
		return task.ErrInvalid
	}
	if cfg.Validate() != nil || q.Version < 0 || enabled && (!task.ValidRef(q.TaskRef) || q.TaskEpoch < 1 || !q.ValidRule()) {
		return task.ErrInvalid
	}
	mode := q.Mode
	if mode == "" {
		mode = "automatic"
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var prior int64
		err := tx.QueryRowContext(ctx, `SELECT version FROM github_task_binding WHERE scope=?`, cfg.Scope()).Scan(&prior)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if prior != q.Version || !enabled && prior == 0 {
			return agent.ErrConflict
		}
		if !enabled {
			return revokeGitHubBinding(ctx, tx, cfg.Scope(), "")
		}
		var id string
		err = tx.QueryRowContext(ctx, `SELECT t.task_id FROM task t JOIN github_watch w ON w.scope=? WHERE t.task_ref=? AND t.task_epoch=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 AND t.conversation_id=t.agent_id AND t.status IN('active','completed') AND t.explicit_end_epoch<t.task_epoch AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0)
 AND w.enabled=1 AND w.repository_id IS NOT NULL AND w.failure_code='' AND w.intent_privacy_epoch=`+githubEpoch, cfg.Scope(), q.TaskRef, q.TaskEpoch).Scan(&id)
		if err == sql.ErrNoRows {
			return agent.ErrConflict
		}
		if err != nil {
			return err
		}
		// cfg is trusted owner composition. Reconcile other scopes only after
		// the requested binding passes its checks, in the same commit.
		if err = revokeOtherGitHubScopes(ctx, tx, configured.encoded); err != nil {
			return err
		}
		if prior != 0 {
			if err = revokeGitHubBinding(ctx, tx, cfg.Scope(), ""); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_task_binding(scope,task_id,task_epoch,privacy_epoch,enabled,generation,last_sequence,version,rule_mode,rule_event_kind,rule_check_conclusion)
 VALUES(?,?,?,`+githubEpoch+`,1,1,(SELECT coalesce(max(sequence),0) FROM github_observation WHERE scope=?),1,?,?,?)
 ON CONFLICT(scope) DO UPDATE SET task_id=excluded.task_id,task_epoch=excluded.task_epoch,privacy_epoch=excluded.privacy_epoch,enabled=1,generation=github_task_binding.generation+1,last_sequence=excluded.last_sequence,version=github_task_binding.version+1,rule_mode=excluded.rule_mode,rule_event_kind=excluded.rule_event_kind,rule_check_conclusion=excluded.rule_check_conclusion`, cfg.Scope(), id, q.TaskEpoch, cfg.Scope(), mode, q.EventKind, q.CheckConclusion)
		if err != nil {
			return err
		}
		return githubWake(ctx, tx, cfg.Scope(), "github.task.bound", map[string]string{"task_ref": q.TaskRef})
	})
	return githubBindingError(err)
}

func githubBindingError(err error) error {
	if err == nil || errors.Is(err, task.ErrInvalid) || errors.Is(err, agent.ErrConflict) {
		return err
	}
	return githubStorageError(err)
}

func revokeGitHubBinding(ctx context.Context, tx *sql.Tx, scope, taskID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE github_task_binding SET enabled=0,generation=generation+1,version=version+1 WHERE enabled=1 AND (?='' OR scope=?) AND (?='' OR task_id=?)`, scope, scope, taskID, taskID)
	if err != nil {
		return err
	}
	return stopInvalidGitHubMains(ctx, tx)
}

func stopInvalidGitHubMains(ctx context.Context, tx *sql.Tx) error {
	now := time.Now().UnixMicro()
	_, err := tx.ExecContext(ctx, `UPDATE run AS r SET desired_state='github_revoked',updated_at=?,version=version+1 WHERE r.status IN('queued','running','waiting') AND r.desired_state='running' AND NOT (`+currentGitHubCause+`)`, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tool_invocation SET desired_state='cancelled',updated_at=?,version=version+1 WHERE status IN('queued','running','waiting') AND desired_state!='cancelled' AND run_id IN(SELECT run_id FROM run WHERE desired_state IN('github_revoked','github_user_superseded'))`, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE github_task_followup SET state='ignored' WHERE state!='ignored' AND run_id IN(SELECT run_id FROM run WHERE desired_state IN('github_revoked','github_user_superseded'))`)
	return err
}

func (s *Store) GitHubTaskBinding(ctx context.Context, cfg github.Config) (v github.TaskBinding, err error) {
	v.State = "unbound"
	v.Mode = "off"
	v.Scope = cfg.Scope()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return v, githubStorageError(err)
	}
	defer tx.Rollback()
	var enabled, current bool
	var mode, goal string
	err = tx.QueryRowContext(ctx, `SELECT t.task_ref,b.task_epoch,b.version,b.enabled,b.last_sequence,b.generation,b.rule_mode,b.rule_event_kind,b.rule_check_conclusion,t.anchor_task,
 b.enabled=1 AND w.enabled=1 AND w.failure_code='' AND b.task_epoch=t.task_epoch AND t.status IN('active','completed') AND t.explicit_end_epoch<t.task_epoch AND b.privacy_epoch=`+githubEpoch+` AND w.intent_privacy_epoch=b.privacy_epoch
 FROM github_task_binding b JOIN task t USING(task_id) JOIN github_watch w USING(scope) WHERE b.scope=?`, cfg.Scope()).Scan(&v.TaskRef, &v.TaskEpoch, &v.Version, &enabled, &v.LastSequence, &v.Generation, &mode, &v.EventKind, &v.CheckConclusion, &goal, &current)
	if err == sql.ErrNoRows {
		return v, nil
	}
	if err != nil {
		return v, githubStorageError(err)
	}
	v.State = "stopped"
	if enabled {
		v.State = "stale"
		v.Mode = mode
	}
	if current {
		v.State = "bound"
	}
	// Old privacy scopes retain only control metadata in this owner projection.
	var privacy bool
	if err = tx.QueryRowContext(ctx, `SELECT privacy_epoch=`+githubEpoch+` FROM github_task_binding WHERE scope=?`, cfg.Scope()).Scan(&privacy); err != nil {
		return v, githubStorageError(err)
	}
	if !privacy {
		v.TaskRef = ""
		v.TaskEpoch = 0
		v.EventKind = ""
		v.CheckConclusion = ""
		v.Mode = "off"
		v.State = "privacy_stopped"
	} else {
		// All facts have been read consistently. Release the reader before
		// isolated policy, then fence the exact binding/privacy before output.
		_ = tx.Rollback()
		v.TaskGoal, err = s.githubBindingGoalPreview(ctx, cfg, v.Version, goal)
		if err != nil {
			return v, githubStorageError(err)
		}
	}
	return v, nil
}
