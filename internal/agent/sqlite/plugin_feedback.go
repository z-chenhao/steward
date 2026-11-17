package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type feedbackPolicy struct {
	managed               bool
	revision, requestHash string
	contract.Policy
}

func validFeedbackPolicy(p contract.Policy) bool {
	return p.LifetimeSeconds >= 1 && p.LifetimeSeconds <= 604800 && len(p.ResponseGuidance) <= 2048 && strings.TrimSpace(p.ResponseGuidance) != "" && utf8.ValidString(p.ResponseGuidance) && !strings.ContainsFunc(p.ResponseGuidance, unicode.IsControl)
}

func (t *tools) prepareFeedbackPolicy(ctx context.Context, inv tool.Invocation) (feedbackPolicy, error) {
	p := feedbackPolicy{Policy: contract.Policy{LifetimeSeconds: 604800, ResponseGuidance: legacyFeedbackGuidanceV1}}
	var err error
	p.managed, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.Contract)
	if err != nil || !p.managed {
		return p, err
	}
	needed := false
	kind := "input"
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		role, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if role != "main" || name != task.ManageName {
			return nil
		}
		q, err := task.ParseManage(args)
		if err != nil || (q.Operation != "request_input" && q.Operation != "request_review") {
			return nil
		}
		canonical, _ := encoded(q)
		p.requestHash = digest(canonical)
		var accepted bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_invocation WHERE invocation_id=?)`, inv.ID).Scan(&accepted); err != nil || accepted {
			return err
		}
		var source string
		if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT i.message_id FROM main_input i WHERE i.run_id=r.run_id AND i.sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY i.sequence DESC LIMIT 1),(SELECT request_id FROM main_inbox b WHERE b.run_id=r.run_id ORDER BY b.sequence DESC LIMIT 1),'') FROM run r WHERE r.run_id=?`, t.claim.RunID()).Scan(&source); err != nil {
			return err
		}
		if source == "" {
			return nil
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_request WHERE source_message_id=? AND request_hash=?)`, source, p.requestHash).Scan(&accepted); err != nil || accepted {
			return err
		}
		allowed, err := t.runs.checkGitHubAppend(ctx, tx, t.claim.RunID(), q)
		if err != nil || !allowed {
			return err
		}
		// Do not dispatch a recipe for a foreign, stopped or privacy-revoked Task.
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task t JOIN run r ON r.agent_id=t.agent_id AND r.conversation_id=t.conversation_id LEFT JOIN memory_privacy p ON p.agent_id=t.agent_id WHERE r.run_id=? AND t.task_ref=? AND t.status IN ('active','completed') AND t.created_at>coalesce(p.updated_at,0))`, t.claim.RunID(), q.TaskRef).Scan(&needed); err != nil || !needed {
			return err
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		for _, ref := range q.ArtifactRefs {
			if _, err = t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, ref, true); err != nil {
				needed = false
				return nil
			}
		}
		if q.Operation == "request_review" {
			kind = "review"
		}
		return nil
	})
	if err != nil || !needed {
		return p, err
	}
	h, a, err := t.pureToolPolicyHost(ctx, inv)
	if err != nil {
		return p, err
	}
	p.revision = a.pluginGraph
	input, _ := json.Marshal(contract.Facts{Kind: kind})
	raw, err := h.ReadService(ctx, p.revision, contract.Contract, input, nil)
	if err != nil {
		if authority := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.runs.evidenceFence(ctx, tx, a) }); authority != nil {
			return p, authority
		}
		return p, err
	}
	if err = rsiplugin.DecodeResult(raw, &p.Policy); err != nil {
		return p, err
	}
	if !validFeedbackPolicy(p.Policy) {
		return p, rsi.ErrInvalid
	}
	return p, nil
}
