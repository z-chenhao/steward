package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/interaction"
	apps "github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

type ToolLimits struct{ Global, Agent, Run, Provider, Tool int }

func (r *Runs) ConfigureToolLimits(l ToolLimits)       { r.limits = l }
func (r *Runs) ConfigureApprovalTTL(ttl time.Duration) { r.approvalTTL = ttl }
func (r *Runs) approvalTime() time.Time {
	if r.approvalClock != nil {
		return r.approvalClock()
	}
	return time.Now()
}
func (r *Runs) approvalDuration() time.Duration {
	if r.approvalTTL > 0 {
		return r.approvalTTL
	}
	return time.Hour
}
func (r *Runs) toolLimits() ToolLimits {
	l := r.limits
	if l.Global < 1 {
		l.Global = 32
	}
	if l.Agent < 1 {
		l.Agent = 24
	}
	if l.Run < 1 {
		l.Run = 4
	}
	if l.Provider < 1 {
		l.Provider = 16
	}
	if l.Tool < 1 {
		l.Tool = 8
	}
	return l
}

// interaction.Approval belongs to the local user control plane, never model context.
// The local CLI is authenticated by ownership of the private data directory.

func (r *Runs) Approvals(ctx context.Context) ([]interaction.Approval, error) {
	if err := r.recoverApprovals(ctx); err != nil {
		return nil, err
	}
	return r.ApprovalView(ctx)
}

// ApprovalView is the pure Web projection. Expiry is shown without recovering
// Runs, enqueueing events, or changing a decision from a browsing request.
func (r *Runs) ApprovalView(ctx context.Context) ([]interaction.Approval, error) {
	return r.approvalView(ctx, "", false, 0)
}

// PendingApprovals is bounded for the attention surface; history inspection
// retains the existing ApprovalView contract.
func (r *Runs) PendingApprovals(ctx context.Context) ([]interaction.Approval, error) {
	return r.approvalView(ctx, "[]", true, 51)
}

// PageApprovals projects only authorized, page-linked decisions and optionally
// all remaining pending actions required by Chat.
func (r *Runs) PageApprovals(ctx context.Context, ids []string, includePending bool) ([]interaction.Approval, error) {
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	return r.approvalView(ctx, string(raw), includePending, 0)
}
func (r *Runs) approvalView(ctx context.Context, ids string, includePending bool, limit int) ([]interaction.Approval, error) {
	var predicate string
	args := []any{sql.Named("now", r.approvalTime().UnixMicro())}
	if ids != "" {
		predicate = "a.approval_id IN (SELECT value FROM json_each(:ids) WHERE type='text')"
		if includePending {
			predicate = "(" + predicate + " OR a.status='pending')"
		}
		args = append(args, sql.Named("ids", ids))
	}
	scoped := ""
	if predicate != "" {
		scoped = " AND " + predicate
	}
	query := `SELECT approval_id,tool_name,arguments_json,frozen_arguments_hash,status,expires_at,action_parameters,mail_review FROM (
 SELECT a.approval_id,i.tool_name,i.arguments_json,a.frozen_arguments_hash,
 CASE WHEN a.status IN ('pending','approved') AND a.expires_at<=:now AND i.status IN ('queued','waiting') AND i.attempt_count=0 THEN 'expired' ELSE a.status END status,
 a.expires_at,a.created_at,coalesce(p.plan_json,'') action_parameters,
 CASE WHEN i.tool_name GLOB 'mcp__apps__b__*__execute' THEN coalesce((
   SELECT json_extract(j.value,'$.mail_review') FROM run r JOIN capability_snapshot cs ON cs.snapshot_id=r.capability_snapshot_id,
   json_each(cs.tools_json) j WHERE r.run_id=i.run_id AND json_extract(j.value,'$.name')=i.tool_name AND json_extract(j.value,'$.provider')='composio'
 ),'') ELSE '' END mail_review FROM approval a JOIN tool_invocation i USING(invocation_id) LEFT JOIN computer_action_plan p ON p.invocation_id=i.invocation_id AND p.request_hash=i.request_hash WHERE a.scope=(SELECT agent_id FROM agent_config WHERE singleton=1)` + scoped + `
 UNION ALL SELECT approval_id,'account_spend',arguments_json,frozen_hash,status,0,created_at,'','' FROM spend_approval a WHERE a.scope=(SELECT agent_id FROM agent_config WHERE singleton=1)` + scoped + `
 ) a`
	if includePending {
		query += " WHERE " + predicate
	}
	query += " ORDER BY created_at,approval_id"
	if limit > 0 {
		query += " LIMIT :limit"
		args = append(args, sql.Named("limit", limit))
	}
	rows, err := r.host.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, agent.ErrStorage
	}
	defer rows.Close()
	out := []interaction.Approval{}
	for rows.Next() {
		var v interaction.Approval
		var mail string
		if rows.Scan(&v.ID, &v.Tool, &v.Arguments, &v.Hash, &v.Status, &v.ExpiresAt, &v.Parameters, &mail) != nil {
			return nil, agent.ErrStorage
		}
		if mail != "" {
			var review apps.FrozenMailReview
			if json.Unmarshal([]byte(mail), &review) != nil {
				return nil, agent.ErrStorage
			}
			v.MailReview = &review
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Decide requires the displayed frozen hash, in addition to the approval ID.
// A duplicate matching decision is idempotent; conflicting callbacks fail.
func (r *Runs) Decide(ctx context.Context, principal, id, hash, decision string) error {
	if decision != "approved" && decision != "denied" {
		return agent.ErrConflict
	}
	if err := r.recoverApprovals(ctx); err != nil {
		return err
	}
	return r.write(ctx, func(tx *sql.Tx) error { return r.decideTx(ctx, tx, principal, id, hash, decision) })
}
func (r *Runs) DecideInbound(ctx context.Context, e interaction.InboundEvent, id, hash, decision string) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if decision != "approved" && decision != "denied" {
		return agent.ErrConflict
	}
	if err := r.recoverApprovals(ctx); err != nil {
		return err
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		content, _ := encoded(e.Content)
		if _, _, err := acceptChannelSource(ctx, tx, e, content, ""); err != nil {
			return err
		}
		var principal string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&principal); err != nil {
			return err
		}
		return r.decideTx(ctx, tx, principal, id, hash, decision)
	})
}
func (r *Runs) decideTx(ctx context.Context, tx *sql.Tx, principal, id, hash, decision string) error {
	if found, err := r.decideSpend(ctx, tx, principal, id, hash, decision); found || err != nil {
		return err
	}
	var status, runID string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM approval WHERE approval_id=? AND scope=? AND frozen_arguments_hash=?`, id, principal, hash).Scan(&status); err != nil {
		return agent.ErrConflict
	}
	if status == decision {
		return nil
	}
	err := tx.QueryRowContext(ctx, `SELECT a.status,i.run_id FROM approval a JOIN tool_invocation i USING(invocation_id) JOIN run r USING(run_id) WHERE a.approval_id=? AND a.scope=? AND r.agent_id=? AND a.frozen_arguments_hash=? AND i.request_hash=? AND i.status='waiting' AND i.wait_reason='approval' AND r.desired_state='running' AND r.status IN ('running','waiting') AND a.expires_at>?`, id, principal, principal, hash, hash, r.approvalTime().UnixMicro()).Scan(&status, &runID)
	if err != nil {
		return agent.ErrConflict
	}
	if status == decision {
		return nil
	}
	if status != "pending" {
		return agent.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE approval SET status=?,decision_source='local_user',decided_at=?,version=version+1 WHERE approval_id=?`, decision, r.approvalTime().UnixMicro(), id); err != nil {
		return err
	}
	if err = wakeApproval(ctx, tx, runID); err != nil {
		return err
	}
	return runEvent(ctx, tx, runID, "approval.decided", 1)
}

func wakeApproval(ctx context.Context, tx *sql.Tx, runID string) error {
	// Only released approval waiters need an explicit wake. Running owners see
	// the decision in Prepare; other wait reasons retain their own wake policy.
	_, err := tx.ExecContext(ctx, `UPDATE run SET status='running',wait_reason=NULL,updated_at=?,version=version+1 WHERE run_id=? AND status='waiting' AND wait_reason='approval' AND desired_state='running'`, time.Now().UnixMicro(), runID)
	return err
}
func (r *Runs) recoverApprovals(ctx context.Context) error {
	return r.write(ctx, func(tx *sql.Tx) error {
		now := r.approvalTime().UnixMicro()
		rows, err := tx.QueryContext(ctx, `UPDATE approval SET status='expired',decided_at=?,version=version+1 WHERE status IN ('pending','approved') AND expires_at<=? AND invocation_id IN (SELECT invocation_id FROM tool_invocation WHERE status IN ('queued','waiting') AND attempt_count=0) RETURNING invocation_id`, now, now)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			var run string
			if err = tx.QueryRowContext(ctx, `SELECT run_id FROM tool_invocation WHERE invocation_id=?`, id).Scan(&run); err != nil {
				return err
			}
			if err = wakeApproval(ctx, tx, run); err != nil {
				return err
			}
			if err = runEvent(ctx, tx, run, "approval.expired", 1); err != nil {
				return err
			}
		}
		return nil
	})
}

// Steering invalidates every unexecuted approval in that Run, including a
// decision received before dispatch. New arguments require a new invocation.
func invalidateApprovals(ctx context.Context, tx *sql.Tx, runID string) error {
	res, err := tx.ExecContext(ctx, `UPDATE approval SET status='invalidated',decided_at=?,version=version+1 WHERE status IN ('pending','approved') AND invocation_id IN (SELECT invocation_id FROM tool_invocation WHERE run_id=? AND attempt_count=0 AND status IN ('queued','waiting'))`, time.Now().UnixMicro(), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err = wakeApproval(ctx, tx, runID); err != nil {
		return err
	}
	return runEvent(ctx, tx, runID, "approval.invalidated", 1)
}

// ReconcileUnknown queries only; it never calls Execute/Invoke. It can settle
// an unknown after its Run ended, without reviving or modifying that Run.
func (r *Runs) ReconcileUnknown(ctx context.Context) error {
	rows, err := r.host.db.QueryContext(ctx, `SELECT i.invocation_id,i.run_id,i.tool_name,i.arguments_json,i.idempotency_key,c.tools_json FROM tool_invocation i JOIN run r USING(run_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE i.status='unknown' ORDER BY i.created_at`)
	if err != nil {
		return agent.ErrStorage
	}
	type item struct{ id, run, name, args, key, specs string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.run, &v.name, &v.args, &v.key, &v.specs); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range items {
		var specs []tool.Spec
		if json.Unmarshal([]byte(v.specs), &specs) != nil {
			return agent.ErrStorage
		}
		current, ok := r.registry.Resolve(v.name)
		if !ok || current.Query == nil {
			// No authoritative receipt query exists. Keep unknown without
			// invoking a method or validating unrelated plugin capabilities.
			continue
		}
		var original *tool.Spec
		for i := range specs {
			if specs[i].Name == v.name {
				original = &specs[i]
				break
			}
		}
		if original == nil {
			// An originally unavailable operation cannot acquire a newly
			// registered tool's query authority. Its uncertainty remains intact.
			continue
		}
		// This scan may run after the Run ended and has no execution claim.
		// Validate only the queried resource against its own frozen identity;
		// other features' managed views cannot authorize or block this query.
		registry, err := r.registry.Select([]tool.Spec{*original})
		if err != nil {
			return err
		}
		d, ok := registry.Resolve(v.name)
		if !ok || d.Query == nil {
			continue
		}
		_, err = tool.RecoverResult(ctx, d, tool.Request{Arguments: v.args, IdempotencyKey: v.key}, func(outputCtx context.Context, result tool.Result) (tool.Result, error) {
			prepared := r.stageOutput(outputCtx, v.id, result)
			defer prepared.Close()
			writeCtx := context.WithoutCancel(outputCtx)
			committed := false
			original := result
			err := r.write(writeCtx, func(tx *sql.Tx) error {
				result = original
				// The query ran outside the lock. Another reconciler may have won;
				// never consume or publish another receipt after terminal commit.
				var status string
				if err := tx.QueryRowContext(writeCtx, `SELECT status FROM tool_invocation WHERE invocation_id=?`, v.id).Scan(&status); err != nil {
					return err
				}
				if status != "unknown" {
					result = tool.Failure("UNKNOWN_SIDE_EFFECT")
					return nil
				}
				var err error
				result, err = r.storeRecoveryOutput(writeCtx, outputCtx, tx, v.run, v.id, result, prepared)
				if err != nil || (result.Error != nil && result.Error.Code == "UNKNOWN_SIDE_EFFECT") {
					return err
				}
				value, _ := encoded(result)
				status = "succeeded"
				var errorJSON any
				if result.Error != nil {
					status = "failed"
					errorJSON, _ = encoded(result.Error)
				}
				resultRef := digest(value)
				if len(result.ArtifactRefs) > 0 {
					resultRef = result.ArtifactRefs[0]
				}
				res, err := tx.ExecContext(writeCtx, `UPDATE tool_invocation SET status=?,result_json=?,result_ref=?,error_json=?,result_ready_at=?,updated_at=?,reconciliation_json=?,version=version+1 WHERE invocation_id=? AND status='unknown'`, status, value, resultRef, errorJSON, time.Now().UnixMicro(), time.Now().UnixMicro(), value, v.id)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return nil
				}
				r.host.boundary("before_tool_commit")
				committed = true
				return runEvent(writeCtx, tx, v.run, "tool_invocation.reconciled", 1)
			})
			if err == nil && committed {
				r.host.boundary("after_tool_commit")
			}
			return result, err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ToolOutcomes exposes query-only reconciliation observations to the local user.
// It does not mutate or redeliver historical model/Conversation messages.

func (r *Runs) ToolOutcomes(ctx context.Context) ([]tool.ToolOutcome, error) {
	rows, err := r.host.db.QueryContext(ctx, `SELECT i.tool_name,i.arguments_json,i.status,coalesce(i.result_json,'{}') FROM tool_invocation i JOIN run r USING(run_id) WHERE r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND (i.status='unknown' OR i.reconciliation_json IS NOT NULL) ORDER BY i.created_at,i.invocation_id`)
	if err != nil {
		return nil, agent.ErrStorage
	}
	defer rows.Close()
	out := []tool.ToolOutcome{}
	for rows.Next() {
		var v tool.ToolOutcome
		var result string
		if err = rows.Scan(&v.Tool, &v.Arguments, &v.Status, &result); err != nil {
			return nil, agent.ErrStorage
		}
		if json.Unmarshal([]byte(result), &v.Result) != nil {
			return nil, agent.ErrStorage
		}
		if v.Status == "unknown" {
			v.Result = tool.Failure("UNKNOWN_SIDE_EFFECT")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
