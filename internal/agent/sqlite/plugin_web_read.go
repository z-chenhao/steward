package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Process-local Host proof; it is neither plugin output nor another journal.
// The existing Tool commit rechecks this reference and its original interpreter
// in the same transaction that makes the read public.
type webReadReceipt struct {
	Ref      string
	Revision string
	Managed  bool
}

// A web publication's immutable Tool producer determines its interpreter.
// Imported/arbitrary JSON uses the reading Run's frozen policy instead. Merely
// having been created during a managed Run does not turn a file into a web ref.
func (t *tools) webReaderRevision(ctx context.Context, tx *sql.Tx, ref string) (managed bool, revision string, err error) {
	var services string
	err = tx.QueryRowContext(ctx, `SELECT coalesce(b.graph_revision,''),coalesce(b.managed_services_json,'[]')
FROM artifact a JOIN tool_invocation i ON a.put_key='tool:'||i.invocation_id AND a.run_id=i.run_id
LEFT JOIN rsi_run b ON b.run_id=i.run_id
WHERE a.artifact_ref=? AND i.status='succeeded' AND i.tool_name IN (?,?,?)`, ref, webaccess.SearchName, webaccess.ContentName, webaccess.CheckName).Scan(&revision, &services)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT coalesce(graph_revision,''),managed_services_json FROM rsi_run WHERE run_id=?`, t.claim.RunID()).Scan(&revision, &services)
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil // explicit pre-plugin execution
		}
	}
	if err != nil {
		return false, "", err
	}
	var contracts []string
	if err = json.Unmarshal([]byte(services), &contracts); err != nil {
		return false, "", err
	}
	for _, contract := range contracts {
		if contract == webaccess.ReadContract {
			return true, revision, nil
		}
	}
	return false, "", nil
}

func (t *tools) executeWebRead(ctx context.Context, inv tool.Invocation, run agent.Run, args string) (tool.Result, error) {
	var input webaccess.ReadArguments
	if json.Unmarshal([]byte(args), &input) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	check := func(c context.Context) error {
		return t.runs.evidenceReadTx(c, func(tx *sql.Tx) error {
			_, name, original, err := t.acceptedBuiltin(c, tx, inv)
			if err != nil {
				return err
			}
			if name != webaccess.ReadName || original != args {
				return tool.ErrApprovalDenied
			}
			if err = t.runs.pluginDispatchFence(c, tx, t.claim.RunID()); err != nil {
				return err
			}
			scope, err := runScope(c, tx, t.claim.RunID())
			if err != nil {
				return err
			}
			_, err = t.runs.host.Artifacts().DescribeTx(c, tx, scope, input.ResponseID, true)
			return err
		})
	}
	// The artifact reader authorizes before opening bytes; check again after IO.
	data, err := (webAccessHost{tools: t, invocation: inv, run: run}).ReadEvidence(ctx, input.ResponseID)
	if err != nil {
		if authority := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.fence(ctx, tx) }); authority != nil {
			return tool.Result{}, authority
		}
		return tool.Failure(err.Error()), nil
	}
	if err = check(ctx); err != nil {
		return tool.Result{}, err
	}
	var document webaccess.Document
	if json.Unmarshal(data, &document) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	var managed bool
	var revision string
	if err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var e error
		managed, revision, e = t.webReaderRevision(ctx, tx, input.ResponseID)
		return e
	}); err != nil {
		return tool.Result{}, err
	}
	if !managed {
		// Only explicit pre-service references/Runs reach the frozen V1 reader.
		value, e := webaccess.ReadContent(document, input.URLIndex, input.QueryIndex, input.Offset, input.Limit, input.FindText, input.FindMode)
		if e != nil {
			return tool.Failure(e.Error()), nil
		}
		if err = check(ctx); err != nil {
			return tool.Result{}, err
		}
		result := webaccess.ToolResult(value)
		result.HostFence = webReadReceipt{Ref: input.ResponseID}
		return result, nil
	}
	text, err := webaccess.EvidenceText(document, input.URLIndex, input.QueryIndex)
	if err != nil {
		return tool.Failure(err.Error()), nil
	}
	q := input.ReadRequest
	if q.Offset < 0 || q.Offset > len(text) || q.Limit < 0 || q.Limit > 32000 || len(q.FindText) > 500 {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if q.FindText != "" && q.FindMode != "" && q.FindMode != "exact" && q.FindMode != "case-insensitive" && q.FindMode != "fuzzy" {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if t.runs.plugins == nil || revision == "" {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	h := t.runs.pluginHostWithFence(t.fence)
	// Two fixed chunks cover the full existing 24 MiB artifact limit including
	// base64/JSON overhead. No re-encoding or stricter JSON parser changes the
	// legacy document interpretation (unknown/duplicate fields included).
	port := rsi.Resource{Contract: webaccess.EvidenceContract,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"part":{"type":"integer","minimum":0,"maximum":1}},"required":["part"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","maxLength":16777216},"total_bytes":{"type":"integer","minimum":0,"maximum":25165824}},"required":["data","total_bytes"],"additionalProperties":false}`),
		Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
			var request webaccess.EvidenceRequest
			if rsiplugin.DecodeResource(raw, &request) != nil || request.Part < 0 || request.Part > 1 || key != fmt.Sprintf("evidence:%d", request.Part) {
				return nil, tool.ErrApprovalDenied
			}
			if err := check(c); err != nil {
				return nil, err
			}
			start := request.Part * webaccess.EvidencePartBytes
			if start > len(data) {
				return nil, tool.ErrApprovalDenied
			}
			return json.Marshal(webaccess.EvidenceReply{Data: data[start:min(len(data), start+webaccess.EvidencePartBytes)], TotalBytes: len(data)})
		}}
	rawInput, _ := json.Marshal(q)
	raw, err := h.DriveService(ctx, revision, webaccess.ReadContract, rawInput, nil, check, []rsi.Resource{port})
	t.runs.host.boundary("after_web_read_method")
	if authority := check(ctx); authority != nil {
		return tool.Result{}, authority
	}
	// This resource is a scoped read, with no external call/spend/effect. Its
	// original safe Tool receipt owns retry; generic broker Unknown is not an
	// assertion that a side effect occurred and must not become a Tool unknown.
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	var plan webaccess.ReadPlan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	value, err := acceptWebReadPlan(text, q, plan)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	result := webaccess.ToolResult(value)
	result.HostFence = webReadReceipt{Ref: input.ResponseID, Revision: revision, Managed: true}
	return result, nil
}

func (t *tools) commitWebRead(ctx context.Context, tx *sql.Tx, id string, receipt webReadReceipt) error {
	if err := t.fence(ctx, tx); err != nil {
		return err
	}
	var args string
	if err := tx.QueryRowContext(ctx, `SELECT arguments_json FROM tool_invocation WHERE invocation_id=? AND run_id=? AND tool_name=? AND status='running'`, id, t.claim.RunID(), webaccess.ReadName).Scan(&args); err != nil {
		return err
	}
	var input webaccess.ReadArguments
	if json.Unmarshal([]byte(args), &input) != nil || input.ResponseID != receipt.Ref {
		return tool.ErrApprovalDenied
	}
	scope, err := runScope(ctx, tx, t.claim.RunID())
	if err != nil {
		return err
	}
	if _, err = t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, receipt.Ref, true); err != nil {
		return err
	}
	managed, revision, err := t.webReaderRevision(ctx, tx, receipt.Ref)
	if err != nil {
		return err
	}
	if managed != receipt.Managed || revision != receipt.Revision {
		return rsi.ErrRevoked
	}
	if err = t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
		return err
	}
	if managed {
		var allowed bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?) AND NOT (SELECT stopped FROM rsi_control WHERE singleton=1)`, revision, revision).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return rsi.ErrRevoked
		}
	}
	return nil
}

// Candidate output carries positions, never evidence text, totals or cursors.
// This proof preserves explicit V1 matching/byte semantics without enforcing
// the historical 8000-byte default or 400-byte context recipe.
func acceptWebReadPlan(text string, q webaccess.ReadRequest, p webaccess.ReadPlan) (webaccess.Slice, error) {
	bad := func() (webaccess.Slice, error) { return webaccess.Slice{}, rsi.ErrInvalid }
	if q.Offset < 0 || q.Offset > len(text) || q.Limit < 0 || q.Limit > 32000 || len(q.FindText) > 500 || p.ErrorCode != "" || p.Offset < 0 || p.NextOffset < p.Offset || p.NextOffset > len(text) || p.NextOffset-p.Offset > 32000 {
		return bad()
	}
	boundary := func(at int) bool { return at == len(text) || utf8.RuneStart(text[at]) }
	if !boundary(p.Offset) || !boundary(p.NextOffset) {
		return bad()
	}
	if q.FindText == "" {
		start := q.Offset
		for start > 0 && start < len(text) && !utf8.RuneStart(text[start]) {
			start--
		}
		if p.Found != nil || p.MatchStart != 0 || p.MatchEnd != 0 || p.Offset != start {
			return bad()
		}
		if q.Limit > 0 {
			end := min(len(text), start+q.Limit)
			for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
				end--
			}
			if p.NextOffset != end {
				return bad()
			}
		} else if p.NextOffset == start && start < len(text) {
			return bad()
		}
	} else {
		start, end, err := webaccess.FindSpanV1(text, q.FindText, q.FindMode)
		if err != nil || p.Found == nil || *p.Found != (start >= 0) {
			return bad()
		}
		if start < 0 {
			if p.Offset != 0 || p.NextOffset != 0 || p.MatchStart != 0 || p.MatchEnd != 0 {
				return bad()
			}
			return webaccess.Slice{TotalBytes: len(text), Found: p.Found}, nil
		}
		if p.MatchStart != start || p.MatchEnd != end || p.Offset > start || p.NextOffset <= start {
			return bad()
		}
		// Fuzzy spans can exceed the original maximum window. Preserve that
		// case while requiring the complete match whenever this window fits it.
		if end-p.Offset <= 32000 && p.NextOffset < end {
			return bad()
		}
	}
	return webaccess.Slice{Text: text[p.Offset:p.NextOffset], Offset: p.Offset, NextOffset: p.NextOffset, TotalBytes: len(text), More: p.NextOffset < len(text), Found: p.Found}, nil
}
