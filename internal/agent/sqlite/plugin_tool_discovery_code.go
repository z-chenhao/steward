package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func (b *codeBridge) discoverSearchVersioned(ctx context.Context, m codemode.Message) (codemode.Reply, error) {
	managed, err := b.t.runs.managedRunService(ctx, b.t.claim.RunID(), contract.Contract)
	if err != nil {
		return codemode.Reply{}, err
	}
	// Actual pre-progressive snapshots keep their independent substring contract.
	progressive := false
	if b.registry != nil {
		d, ok := b.registry.Resolve(tool.SearchName)
		progressive = ok && d.Spec.Exposure == tool.ModelOnly
	}
	if !managed || !progressive {
		return b.discover(m), nil
	}
	var args []json.RawMessage
	if json.Unmarshal(m.Arguments, &args) != nil || len(args) == 0 {
		return denied("INVALID_ARGUMENT"), nil
	}
	var query string
	if json.Unmarshal(args[0], &query) != nil {
		return denied("INVALID_ARGUMENT"), nil
	}
	options := struct {
		Limit     int    `json:"limit"`
		Namespace string `json:"namespace"`
	}{Limit: tool.DefaultSearchLimit}
	if len(args) > 1 && json.Unmarshal(args[1], &options) != nil || options.Limit < 1 || options.Limit > tool.MaxSearchLimit {
		return denied("INVALID_ARGUMENT"), nil
	}
	candidates := []tool.Spec{}
	for _, s := range b.req.Tools {
		if codemode.ComputerBridge(s.Name) || !s.ScriptCallable() || options.Namespace != "" && !codemode.NamespaceMatches(s.Name, options.Namespace) {
			continue
		}
		s.Exposure = tool.Deferred
		candidates = append(candidates, s)
	}
	// The exact accepted query/options are sufficient: candidate metadata and
	// role are already fixed by this execution's immutable code_manifest.
	key := digest(contract.Contract + ":" + string(m.Arguments))
	cached := ""
	err = b.t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := b.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT reply_json FROM code_search_reply WHERE execution_id=? AND request_hash=?`, b.req.ExecutionID, key).Scan(&cached)
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	})
	if err != nil {
		return codemode.Reply{}, err
	}
	if cached != "" {
		return decodeDiscoveryReply(cached)
	}
	reply := denied("PROVIDER_UNAVAILABLE")
	matches, err := b.t.managedToolDiscovery(ctx, candidates, nil, query, options.Limit, b.fence)
	if err == nil {
		reply = codemode.Reply{Value: matches.Tools}
	} else if !toolDiscoveryUnavailable(err) {
		return codemode.Reply{}, err
	}
	raw, err := encoded(reply)
	if err != nil {
		return codemode.Reply{}, err
	}
	err = b.t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := b.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT reply_json FROM code_search_reply WHERE execution_id=? AND request_hash=?`, b.req.ExecutionID, key).Scan(&cached)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if err := b.t.runs.pluginDispatchFence(ctx, tx, b.req.RunID); err != nil {
			if !errors.Is(err, rsi.ErrRevoked) {
				return err
			}
			raw, _ = encoded(denied("PROVIDER_UNAVAILABLE"))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO code_search_reply(execution_id,request_hash,reply_json) VALUES(?,?,?)`, b.req.ExecutionID, key, raw); err != nil {
			return err
		}
		cached = raw
		return nil
	})
	if err != nil {
		return codemode.Reply{}, err
	}
	return decodeDiscoveryReply(cached)
}
