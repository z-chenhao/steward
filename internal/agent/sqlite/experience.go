package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ReviewExperience is a trusted local Reviewer operation, never an ordinary
// model capability. Evidence must be a scoped successful test ToolInvocation.
func (r *Runs) ReviewExperience(ctx context.Context, c skill.Config, q skill.ReviewRequest) (map[string]any, error) {
	if !q.Valid() {
		return nil, agent.ErrConflict
	}
	if q.Operation == "list" {
		rows, e := r.host.db.QueryContext(ctx, `SELECT candidate_id,name,status,version FROM skill_candidate WHERE NOT `+forgottenCorrection+` ORDER BY created_at,candidate_id`)
		if e != nil {
			return nil, agent.ErrStorage
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, name, status string
			var version int64
			if rows.Scan(&id, &name, &status, &version) != nil {
				return nil, agent.ErrStorage
			}
			items = append(items, map[string]any{"candidate_ref": "experience:" + id, "name": name, "status": status, "version": version})
		}
		return map[string]any{"candidates": items}, rows.Err()
	}
	originalProcedure := q.Procedure
	policy := experiencePolicy{}
	if q.Operation == "propose" {
		var err error
		policy, err = r.prepareExperiencePolicy(ctx, q.Reason)
		if err != nil {
			return nil, err
		}
		if policy.Introduction != "" {
			q.Procedure = policy.Introduction + "\n\n" + originalProcedure
		}
		if !q.Valid() {
			return nil, agent.ErrConflict
		}
	}
	result := map[string]any{}
	e := r.write(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		if q.Operation == "propose" {
			var proposalErr error
			result, proposalErr = r.proposeExperience(ctx, tx, owner, q, originalProcedure, policy)
			return proposalErr
		}
		id := strings.TrimPrefix(q.CandidateRef, "experience:")
		var name, description, procedure, reason, status, hash, refs string
		var version int64
		if e := tx.QueryRowContext(ctx, `SELECT name,description,procedure,reason,status,method_hash,source_refs,version FROM skill_candidate WHERE candidate_id=? AND agent_id=?`, id, owner).Scan(&name, &description, &procedure, &reason, &status, &hash, &refs, &version); e != nil {
			return e
		}
		if q.Operation == "view" || q.Operation == "verify" || q.Operation == "promote" {
			var hidden bool
			if e := tx.QueryRowContext(ctx, "SELECT "+forgottenCorrection+" FROM skill_candidate WHERE candidate_id=?", id).Scan(&hidden); e != nil {
				return e
			}
			if hidden {
				return agent.ErrConflict
			}
		}
		if q.Version != version {
			return agent.ErrConflict
		}
		if q.Operation == "view" {
			var evidence []string
			if err := json.Unmarshal([]byte(refs), &evidence); err != nil {
				return err
			}
			result = map[string]any{"candidate_ref": q.CandidateRef, "version": version, "name": name, "description": description, "procedure": procedure, "status": status, "source_refs": evidence}
			return nil
		}

		var binding experiencePolicy
		var revision sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT managed,graph_revision,required_runs,introduction FROM experience_policy_binding WHERE candidate_id=?`, id).Scan(&binding.Managed, &revision, &binding.RequiredRuns, &binding.Introduction); err != nil {
			return err
		}
		binding.Revision = revision.String
		next := status
		switch q.Operation {
		case "verify":
			if status != "candidate" && status != "verified" {
				return agent.ErrConflict
			}
			var runID, raw string
			if e := tx.QueryRowContext(ctx, `SELECT i.run_id,i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=? AND i.tool_name='builtin__bash' AND json_extract(i.arguments_json,'$.command')='go test -race ./...' AND i.status='succeeded' AND r.agent_id=? AND r.status='completed'`, strings.TrimPrefix(q.VerificationRef, "invocation:"), owner).Scan(&runID, &raw); e != nil {
				return e
			}
			var value tool.Result
			var proof struct {
				ExitCode  *int   `json:"exit_code"`
				TimedOut  *bool  `json:"timed_out"`
				Command   string `json:"command"`
				Truncated *bool  `json:"truncated"`
			}
			if json.Unmarshal([]byte(raw), &value) != nil || value.Error != nil || json.Unmarshal([]byte(value.Text), &proof) != nil || proof.ExitCode == nil || *proof.ExitCode != 0 || proof.TimedOut == nil || *proof.TimedOut || proof.Truncated == nil || *proof.Truncated || proof.Command != "go test -race ./..." {
				return agent.ErrConflict
			}
			if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO skill_verification VALUES(?,?,?,?,?)`, id, strings.TrimPrefix(q.VerificationRef, "invocation:"), runID, hash, time.Now().UnixMicro()); e != nil {
				return e
			}
			var count int
			if e := tx.QueryRowContext(ctx, `SELECT count(DISTINCT run_id) FROM skill_verification WHERE candidate_id=? AND method_hash=?`, id, hash).Scan(&count); e != nil {
				return e
			}
			needed := binding.RequiredRuns
			if count >= needed {
				next = "verified"
			}
		case "promote":
			if binding.Managed {
				if err := experiencePolicyFence(ctx, tx, binding); err != nil {
					return err
				}
			}
			if status != "verified" {
				return agent.ErrConflict
			}
			var other bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_candidate WHERE agent_id=? AND name=? AND candidate_id!=? AND status='promoted')`, owner, name, id).Scan(&other); err != nil {
				return err
			}
			if other {
				return agent.ErrConflict
			}
			if e := skill.Publish(ctx, c, id, name, description, procedure); e != nil {
				if errors.Is(e, skill.ErrCapacity) {
					return agent.ErrResourceLimit
				}
				return e
			}
			next = "promoted"
		case "reject":
			if status != "candidate" && status != "verified" {
				return agent.ErrConflict
			}
			next = "rejected"
		case "retire":
			if status != "promoted" {
				return agent.ErrConflict
			}
			if e := skill.Retire(c, id, name, description, procedure); e != nil {
				return e
			}
			next = "retired"
		default:
			return agent.ErrConflict
		}
		if _, e := tx.ExecContext(ctx, `UPDATE skill_candidate SET status=?,version=version+1,updated_at=? WHERE candidate_id=? AND version=?`, next, time.Now().UnixMicro(), id, version); e != nil {
			return e
		}
		result = map[string]any{"candidate_ref": q.CandidateRef, "status": next, "version": version + 1}
		return runEvent(ctx, tx, id, "skill."+next, version+1)
	})
	if errors.Is(e, errExperienceUnavailable) {
		return nil, errExperienceUnavailable
	}
	return result, e
}

func (r *Runs) proposeExperience(ctx context.Context, tx *sql.Tx, owner string, q skill.ReviewRequest, originalProcedure string, policy experiencePolicy) (map[string]any, error) {
	if err := experiencePolicyFence(ctx, tx, policy); err != nil {
		return nil, err
	}
	sourceUser, sourceFailure := false, false
	for _, ref := range q.SourceRefs {
		var n int
		if strings.HasPrefix(ref, "conversation:") {
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM conversation_intent WHERE message_id=? AND json_extract(message_json,'$.conversation_id')=?`, strings.TrimPrefix(ref, "conversation:"), owner).Scan(&n); e != nil {
				return nil, e
			}
		} else if strings.HasPrefix(ref, "run:") {
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE run_id=? AND agent_id=? AND status IN('completed','failed')`, strings.TrimPrefix(ref, "run:"), owner).Scan(&n); e != nil {
				return nil, e
			}
		}
		if n != 1 {
			return nil, agent.ErrConflict
		}
		if strings.HasPrefix(ref, "conversation:") {
			var user bool
			if e := tx.QueryRowContext(ctx, `SELECT json_extract(message_json,'$.role')='user' FROM conversation_intent WHERE message_id=?`, strings.TrimPrefix(ref, "conversation:")).Scan(&user); e != nil {
				return nil, e
			}
			sourceUser = sourceUser || user
		}
		if strings.HasPrefix(ref, "run:") {
			var failed bool
			if e := tx.QueryRowContext(ctx, `SELECT status='failed' FROM run WHERE run_id=?`, strings.TrimPrefix(ref, "run:")).Scan(&failed); e != nil {
				return nil, e
			}
			sourceFailure = sourceFailure || failed
		}
	}
	if q.Reason == "correction" && !sourceUser || q.Reason == "recovered_failure" && !sourceFailure {
		return nil, agent.ErrConflict
	}
	id := newID()
	now := time.Now().UnixMicro()
	refs, _ := encoded(q.SourceRefs)
	hash := digest(q.Procedure)
	if policy.Managed {
		var err error
		hash, err = rsiplugin.Digest("steward.experience-method/1", struct {
			Procedure    string `json:"procedure"`
			Revision     string `json:"policy_revision"`
			RequiredRuns int    `json:"required_runs"`
		}{q.Procedure, policy.Revision, policy.RequiredRuns})
		if err != nil {
			return nil, err
		}
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO skill_candidate VALUES(?,?,?,?,?,?,?,?, 'candidate',?,?,1) ON CONFLICT(agent_id,name,method_hash) DO NOTHING`, id, owner, q.Name, q.Description, q.Procedure, q.Reason, refs, hash, now, now); e != nil {
		return nil, e
	}
	if e := tx.QueryRowContext(ctx, `SELECT candidate_id,status,version FROM skill_candidate WHERE agent_id=? AND name=? AND method_hash=?`, owner, q.Name, hash).Scan(&id, &q.Operation, &q.Version); e != nil {
		return nil, e
	}
	var previousDescription, previousReason, previousRefs string
	if e := tx.QueryRowContext(ctx, `SELECT description,reason,source_refs FROM skill_candidate WHERE candidate_id=?`, id).Scan(&previousDescription, &previousReason, &previousRefs); e != nil {
		return nil, e
	}
	if previousDescription != q.Description || previousReason != q.Reason || previousRefs != refs {
		return nil, agent.ErrConflict
	}
	var revision any
	if policy.Managed {
		revision = policy.Revision
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO experience_policy_binding(candidate_id,managed,graph_revision,required_runs,introduction,original_procedure) SELECT ?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM experience_policy_binding WHERE candidate_id=?)`, id, policy.Managed, revision, policy.RequiredRuns, policy.Introduction, originalProcedure, id); err != nil {
		return nil, err
	}
	return map[string]any{"candidate_ref": "experience:" + id, "status": q.Operation, "version": q.Version}, runEvent(ctx, tx, id, "skill.candidate", q.Version)
}
