package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (t *tools) searchTools(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	result := tool.Result{}
	var request tool.SearchRequest
	var specs []tool.Spec
	var cp agent.Checkpoint
	var frozen, checkpoint string
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		_, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != tool.SearchName || json.Unmarshal([]byte(args), &request) != nil || strings.TrimSpace(request.Query) == "" {
			result = tool.Failure("INVALID_ARGUMENT")
			return nil
		}
		if request.Limit == 0 {
			request.Limit = tool.DefaultSearchLimit
		}
		if request.Limit < 1 || request.Limit > tool.MaxSearchLimit {
			result = tool.Failure("INVALID_ARGUMENT")
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT c.tools_json,r.checkpoint_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, t.claim.RunID()).Scan(&frozen, &checkpoint); err != nil {
			return err
		}
		return nil
	})
	if err != nil || result.Error != nil {
		return result, err
	}
	// The immutable snapshot and this logical call's checkpoint were captured
	// together. Decode their large bytes after releasing the state writer.
	if json.Unmarshal([]byte(frozen), &specs) != nil || json.Unmarshal([]byte(checkpoint), &cp) != nil {
		return tool.Result{}, agent.ErrStorage
	}
	// Ranking is pure frozen metadata work. Do not hold SQLite's write lock
	// while examining a large catalog; new user inputs must remain admissible.
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Contract)
	if err != nil {
		return tool.Result{}, err
	}
	var matches tool.SearchResult
	if !managed {
		matches = tool.SearchToolsLegacyV1(specs, cp.LoadedTools, request.Query, request.Limit)
	} else {
		matches, err = t.managedToolDiscovery(ctx, specs, cp.LoadedTools, request.Query, request.Limit, func(c context.Context, tx *sql.Tx) error { return t.toolDiscoveryAcceptedFence(c, tx, inv) })
		if err != nil {
			if toolDiscoveryUnavailable(err) {
				return tool.Failure("PROVIDER_UNAVAILABLE"), nil
			}
			return tool.Result{}, err
		}
	}
	text, err := encoded(matches)
	return tool.Result{Text: text}, err
}

// Admission already checked capability membership and argument identity. Both
// the capability blob and the Run/invocation references are immutable by SQL
// triggers. Recheck mutable claim/privacy/invocation authority on every chunk,
// without reparsing the whole capability JSON for every256KiB of metadata.
func (t *tools) toolDiscoveryAcceptedFence(ctx context.Context, tx *sql.Tx, inv tool.Invocation) error {
	if err := t.fence(ctx, tx); err != nil {
		return err
	}
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT tool_name FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status='running'`, inv.ID, t.claim.RunID()).Scan(&name); err != nil {
		return err
	}
	if name != tool.SearchName {
		return tool.ErrApprovalDenied
	}
	return nil
}
