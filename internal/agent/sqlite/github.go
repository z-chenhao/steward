package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/event"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

const githubEpoch = `coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)),0)`

func githubStorageError(err error) error {
	var failure *github.Failure
	if err == nil || errors.Is(err, github.ErrStopped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &failure) {
		return err
	}
	return &agent.OperationError{Operation: "GitHub observation", Category: agent.ErrStorage, Cause: err}
}

// Reading PR conversation comments requires the exact live rule that opted in.
const githubIssueCommentRule = `EXISTS(SELECT 1 FROM github_task_binding b JOIN task t USING(task_id)
 WHERE b.scope=w.scope AND b.generation=? AND b.enabled=1 AND b.rule_event_kind='issue_comment'
 AND b.privacy_epoch=w.intent_privacy_epoch AND b.task_epoch=t.task_epoch
 AND t.status IN ('active','completed') AND t.explicit_end_epoch<t.task_epoch
 AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.conversation_id=t.agent_id
 AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0))`

func (s *Store) BeginGitHubPoll(ctx context.Context, cfg github.Config) (ticket github.Ticket, err error) {
	if err = cfg.Validate(); err != nil {
		return ticket, err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO github_watch(scope,repository,pull_number,enabled,intent_privacy_epoch,state,version) VALUES(?,?,?,1,`+githubEpoch+`,'never_checked',1) ON CONFLICT(scope) DO NOTHING`, cfg.Scope(), cfg.Repository, cfg.PullNumber); e != nil {
			return e
		}
		var enabled, valid bool
		if e := tx.QueryRowContext(ctx, `SELECT enabled,intent_privacy_epoch=`+githubEpoch+`,version,intent_privacy_epoch,
 coalesce((SELECT generation FROM github_task_binding b WHERE b.scope=w.scope AND b.enabled=1),0)
 FROM github_watch w WHERE scope=?`, cfg.Scope()).Scan(&enabled, &valid, &ticket.Version, &ticket.Epoch, &ticket.BindingGeneration); e != nil {
			return e
		}
		if !enabled || !valid {
			return github.ErrStopped
		}
		var e error
		if ticket.Managed, e = managedService(ctx, tx, contract.Contract); e != nil {
			return e
		}
		if ticket.Managed {
			if e = tx.QueryRowContext(ctx, `SELECT `+githubIssueCommentRule+` FROM github_watch w WHERE w.scope=?`, ticket.BindingGeneration, cfg.Scope()).Scan(&ticket.IncludeIssueComments); e != nil {
				return e
			}
			if e = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&ticket.GraphRevision); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE github_watch SET last_attempt_at=?,state='checking',version=version+1 WHERE scope=? AND version=?`, time.Now().UnixMicro(), cfg.Scope(), ticket.Version)
		ticket.Version++
		return e
	})
	return ticket, githubStorageError(err)
}

func (s *Store) FinishGitHubPoll(ctx context.Context, cfg github.Config, ticket github.Ticket, batch github.Batch, failure string) error {
	var identityChanged bool
	var methodUnavailable bool
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var valid bool
		var repositoryID sql.NullInt64
		var previousFailure string
		if e := tx.QueryRowContext(ctx, `SELECT enabled=1 AND version=? AND intent_privacy_epoch=? AND intent_privacy_epoch=`+githubEpoch+`,repository_id,failure_code FROM github_watch WHERE scope=?`, ticket.Version, ticket.Epoch, cfg.Scope()).Scan(&valid, &repositoryID, &previousFailure); e != nil {
			return e
		}
		if !valid {
			return github.ErrStopped
		}
		if ticket.IncludeIssueComments {
			var allowed bool
			if e := tx.QueryRowContext(ctx, `SELECT `+githubIssueCommentRule+` FROM github_watch w WHERE w.scope=?`, ticket.BindingGeneration, cfg.Scope()).Scan(&allowed); e != nil {
				return e
			}
			if !allowed {
				return github.ErrStopped
			}
		}
		var interval any
		if ticket.Managed && batch.IntervalSeconds != 0 {
			if batch.IntervalSeconds < 5 || batch.IntervalSeconds > 3600 || cfg.PollSeconds != 0 && batch.IntervalSeconds != cfg.PollSeconds {
				return &github.Failure{Code: "INVALID_METHOD"}
			}
			interval = batch.IntervalSeconds
		}
		now := time.Now().UnixMicro()
		if failure == "" && (batch.RepositoryID <= 0 || batch.HeadSHA == "") {
			return &github.Failure{Code: "INVALID_RESPONSE"}
		}
		if failure == "" && repositoryID.Valid && repositoryID.Int64 != batch.RepositoryID {
			failure = "REPOSITORY_IDENTITY_CHANGED"
			identityChanged = true
		}
		if failure == "" && ticket.Managed {
			if interval == nil {
				return &github.Failure{Code: "INVALID_METHOD"}
			}
			if e := githubMethodFence(ctx, tx, ticket.GraphRevision); e != nil {
				if !errors.Is(e, rsi.ErrRevoked) {
					return e
				}
				failure = "METHOD_UNAVAILABLE"
				methodUnavailable = true
			}
		}
		if failure != "" {
			if _, e := tx.ExecContext(ctx, `UPDATE github_watch SET state='interrupted',failure_code=?,selected_interval_seconds=coalesce(?,selected_interval_seconds),version=version+1 WHERE scope=?`, failure, interval, cfg.Scope()); e != nil {
				return e
			}
			// Poll health gates new admission. Only explicit identity/credential
			// rejection revokes already accepted evidence; a network failure,
			// ambiguous 403/429, 404 or redirect is not an owner revocation.
			if failure == "REPOSITORY_IDENTITY_CHANGED" || failure == "CREDENTIAL_REJECTED" {
				if e := revokeGitHubBinding(ctx, tx, cfg.Scope(), ""); e != nil {
					return e
				}
			}
			if failure != previousFailure {
				return githubWake(ctx, tx, cfg.Scope(), "github.watch.interrupted", map[string]string{"code": failure})
			}
			return nil
		}
		for _, observation := range batch.Events {
			if observation.Kind == "issue_comment" && !(ticket.Managed && ticket.IncludeIssueComments) {
				return &github.Failure{Code: "INVALID_METHOD"}
			}
			var method any
			if ticket.Managed {
				if observation.Method == nil || observation.Method.GraphRevision != ticket.GraphRevision || !github.ValidRecipe(observation.Method.Recipe) {
					return &github.Failure{Code: "INVALID_METHOD"}
				}
				encoded, e := json.Marshal(observation.Method)
				if e != nil {
					return e
				}
				method = string(encoded)
			} else if observation.Method != nil {
				return &github.Failure{Code: "INVALID_METHOD"}
			}
			observation.Sequence = 0
			observation.ReceivedAt = nil
			raw, e := json.Marshal(observation)
			if e != nil {
				return e
			}
			result, e := tx.ExecContext(ctx, `INSERT INTO github_observation(scope,event_key,kind,source_id,content_json,received_at,intent_privacy_epoch,binding_generation,source_method_json) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(scope,event_key) DO NOTHING`, cfg.Scope(), observation.Key, observation.Kind, observation.SourceID, string(raw), now, ticket.Epoch, ticket.BindingGeneration, method)
			if e != nil {
				return e
			}
			count, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if count != 0 {
				if ticket.BindingGeneration != 0 {
					sequence, e := result.LastInsertId()
					if e != nil {
						return e
					}
					if e = recordGitHubRuleDecision(ctx, tx, cfg.Scope(), ticket, sequence, observation); e != nil {
						return e
					}
				}
				if e = githubWake(ctx, tx, cfg.Scope(), "github.observation.received", map[string]string{"event_key": observation.Key, "kind": observation.Kind}); e != nil {
					return e
				}
			}
		}
		_, e := tx.ExecContext(ctx, `UPDATE github_watch SET repository_id=?,state='healthy',failure_code='',head_sha=?,last_success_at=?,selected_interval_seconds=?,version=version+1 WHERE scope=?`, batch.RepositoryID, batch.HeadSHA, now, interval, cfg.Scope())
		if e == nil && previousFailure != "" {
			return githubWake(ctx, tx, cfg.Scope(), "github.watch.recovered", map[string]string{"state": "healthy"})
		}
		return e
	})
	if err == nil && identityChanged {
		return &github.Failure{Code: "REPOSITORY_IDENTITY_CHANGED"}
	}
	if err == nil && methodUnavailable {
		return &github.Failure{Code: "METHOD_UNAVAILABLE"}
	}
	return githubStorageError(err)
}

func githubWake(ctx context.Context, tx *sql.Tx, scope, kind string, data any) error {
	e, err := event.New("/steward/github", kind, scope, data)
	if err != nil {
		return err
	}
	return eventsqlite.Enqueue(ctx, tx, e)
}

// SetGitHubWatch is an explicit installation-owner control, never a model Tool.
func (s *Store) SetGitHubWatch(ctx context.Context, cfg github.Config, enabled bool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := revokeGitHubBinding(ctx, tx, cfg.Scope(), ""); err != nil {
			return err
		}
		state := "stopped"
		if enabled {
			state = "never_checked"
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO github_watch(scope,repository,pull_number,enabled,intent_privacy_epoch,state,version) VALUES(?,?,?,?,`+githubEpoch+`,?,1) ON CONFLICT(scope) DO UPDATE SET enabled=excluded.enabled,intent_privacy_epoch=excluded.intent_privacy_epoch,state=excluded.state,failure_code='',head_sha='',last_success_at=NULL,last_attempt_at=NULL,version=github_watch.version+1`, cfg.Scope(), cfg.Repository, cfg.PullNumber, enabled, state)
		if e != nil {
			return e
		}
		return githubWake(ctx, tx, cfg.Scope(), "github.watch.configured", map[string]bool{"enabled": enabled})
	})
	return githubStorageError(err)
}

func (s *Store) GitHubStatus(ctx context.Context, cfg github.Config) (status github.Status, err error) {
	status = github.Status{Repository: cfg.Repository, PullNumber: cfg.PullNumber, Enabled: true, State: "never_checked"}
	var attempt, success, repositoryID sql.NullInt64
	var selectedInterval sql.NullInt64
	var current bool
	err = s.db.QueryRowContext(ctx, `SELECT repository_id,enabled,state,failure_code,head_sha,last_attempt_at,last_success_at,intent_privacy_epoch=`+githubEpoch+`,(SELECT count(*) FROM github_observation o WHERE o.scope=w.scope AND o.intent_privacy_epoch=`+githubEpoch+`),selected_interval_seconds FROM github_watch w WHERE scope=?`, cfg.Scope()).Scan(&repositoryID, &status.Enabled, &status.State, &status.FailureCode, &status.HeadSHA, &attempt, &success, &current, &status.EventCount, &selectedInterval)
	if err == sql.ErrNoRows {
		return status, nil
	}
	if err != nil {
		return status, githubStorageError(err)
	}
	if repositoryID.Valid {
		status.RepositoryID = &repositoryID.Int64
	}
	if attempt.Valid {
		v := time.UnixMicro(attempt.Int64).UTC()
		status.LastAttempt = &v
	}
	if success.Valid {
		v := time.UnixMicro(success.Int64).UTC()
		status.LastSuccess = &v
	}
	interval := cfg.Interval()
	if selectedInterval.Valid {
		interval = time.Duration(selectedInterval.Int64) * time.Second
	}
	if !current {
		status.State = "privacy_stopped"
		status.Enabled = false
		status.HeadSHA = ""
	} else if status.Enabled && (status.State == "healthy" && time.Since(*status.LastSuccess) > 2*interval || status.State == "checking" && time.Since(*status.LastAttempt) > 2*interval) {
		status.State = "stale"
	}
	return
}

func (s *Store) GitHubObservations(ctx context.Context, cfg github.Config, before int64) ([]github.Observation, error) {
	if before < 0 {
		return nil, fmt.Errorf("invalid GitHub observation cursor")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,content_json,received_at FROM github_observation WHERE scope=? AND intent_privacy_epoch=`+githubEpoch+` AND (?=0 OR sequence<?) ORDER BY sequence DESC LIMIT 100`, cfg.Scope(), before, before)
	if err != nil {
		return nil, githubStorageError(err)
	}
	defer rows.Close()
	observations := []github.Observation{}
	for rows.Next() {
		var observation github.Observation
		var raw string
		var sequence, received int64
		if err = rows.Scan(&sequence, &raw, &received); err != nil {
			return nil, githubStorageError(err)
		}
		if json.Unmarshal([]byte(raw), &observation) != nil {
			return nil, agent.ErrStorage
		}
		observation.Sequence = sequence
		at := time.UnixMicro(received).UTC()
		observation.ReceivedAt = &at
		observations = append(observations, observation)
	}
	return observations, githubStorageError(rows.Err())
}
