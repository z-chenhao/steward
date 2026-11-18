package sqlite

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/tool"
	"testing"
	"time"
)

func TestWebApprovalViewDoesNotRecoverOrWakeExpiredWork(t *testing.T) {
	ctx := context.Background()
	calls := effectCall(`{"target":"Alex","text":"Review this action"}`)
	s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{effectDef(t, "query_before_retry")}, calls)
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	r.approvalClock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	events := scalar(t, s, `SELECT count(*) FROM event_outbox`)
	views, err := r.ApprovalView(ctx)
	if err != nil || len(views) != 1 || views[0].Status != "expired" {
		t.Fatal(views, err)
	}
	if pending, err := r.PendingApprovals(ctx); err != nil || len(pending) != 0 {
		t.Fatal("expired action remained in attention", pending, err)
	}
	if scalar(t, s, `SELECT count(*) FROM approval WHERE status='pending'`) != 1 || scalar(t, s, `SELECT count(*) FROM event_outbox`) != events || scalar(t, s, `SELECT sum(attempt_count) FROM tool_invocation`) != 0 {
		t.Fatal("browsing recovered or dispatched an action")
	}
	if _, err = r.Approvals(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM approval WHERE status='expired'`) != 1 {
		t.Fatal("CLI/runtime recovery behavior changed")
	}
}

func TestPageApprovalsTenThousandDecisionsAndOnePending(t *testing.T) {
	ctx := context.Background()
	calls := effectCall(`{"target":"Alex","text":"Visible pending"}`)
	s, r, _, executor, exchange := admitPolicy(t, []tool.Definition{effectDef(t, "query_before_retry")}, calls)
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	const n = 10000
	if _, err := s.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
	INSERT INTO tool_invocation(invocation_id,run_id,provider_exchange_id,provider_call_id,tool_name,arguments_json,request_hash,replay_policy,idempotency_key,status,delivery_seq,created_at,updated_at,version)
	SELECT 'old-'||i,run_id,provider_exchange_id,'call-'||i,tool_name,arguments_json,request_hash,replay_policy,'old-'||i,'queued',i,created_at,updated_at,1 FROM n CROSS JOIN (SELECT * FROM tool_invocation LIMIT 1)`, n); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO approval SELECT i.invocation_id,i.invocation_id,'high',a.scope,i.request_hash,'pending',NULL,a.expires_at,NULL,i.created_at,1 FROM tool_invocation i CROSS JOIN (SELECT * FROM approval LIMIT 1) a WHERE i.invocation_id LIKE 'old-%';
	UPDATE approval SET status='denied',decision_source='local_user',decided_at=created_at,version=2 WHERE approval_id LIKE 'old-%'`); err != nil {
		t.Fatal(err)
	}
	chat, err := r.PageApprovals(ctx, []string{"old-185", "absent"}, true)
	if err != nil || len(chat) != 2 {
		t.Fatal(len(chat), err)
	}
	task, err := r.PageApprovals(ctx, []string{"old-185"}, false)
	if err != nil || len(task) != 1 || task[0].ID != "old-185" || task[0].Status != "denied" {
		t.Fatal(task, err)
	}
	none, err := r.PageApprovals(ctx, nil, false)
	if err != nil || len(none) > 0 {
		t.Fatal(none, err)
	}
}
