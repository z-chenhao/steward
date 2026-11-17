package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/codemodepolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func codeLimitFacts(l codemode.Limits) contract.Limits {
	return contract.Limits{TimeoutMS: l.TimeoutMS, MemoryBytes: l.MemoryBytes, OutputBytes: l.OutputBytes, PendingCalls: l.PendingCalls, InterruptBudget: l.InterruptBudget}
}
func codeRuntimeLimits(l contract.Limits) codemode.Limits {
	return codemode.Limits{TimeoutMS: l.TimeoutMS, MemoryBytes: l.MemoryBytes, OutputBytes: l.OutputBytes, PendingCalls: l.PendingCalls, InterruptBudget: l.InterruptBudget}
}
func validCodeLimits(l, ceil codemode.Limits) bool {
	return l.TimeoutMS > 0 && l.TimeoutMS <= ceil.TimeoutMS && l.MemoryBytes >= 1<<20 && l.MemoryBytes <= ceil.MemoryBytes && l.OutputBytes > 0 && l.OutputBytes <= ceil.OutputBytes && l.PendingCalls > 0 && l.PendingCalls <= ceil.PendingCalls && l.InterruptBudget > 0 && l.InterruptBudget <= ceil.InterruptBudget
}

// Existing code_manifest already freezes the complete runtime request including
// its limits. Restore only this accepted policy field; source, runtime bytes,
// model and capabilities still pass the original drift check in codeBridge.claim.
func (t *tools) prepareCodeLimits(ctx context.Context, inv tool.Invocation, req *codemode.ExecutionRequest) error {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.LimitsContract)
	if err != nil || !managed {
		return err
	}
	role, cached := "", ""
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != codemode.ToolName {
			return tool.ErrApprovalDenied
		}
		var source struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(args), &source) != nil || digest(source.Source) != req.SourceHash {
			return tool.ErrReplay
		}
		role = kind
		err = tx.QueryRowContext(ctx, `SELECT request_json FROM code_manifest WHERE execution_id=?`, inv.ID).Scan(&cached)
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if cached != "" {
		var saved codemode.ExecutionRequest
		if json.Unmarshal([]byte(cached), &saved) != nil || !validCodeLimits(saved.Limits, codemode.DefaultLimits()) {
			return tool.ErrReplay
		}
		req.Limits = saved.Limits
		return nil
	}
	if t.runs.plugins == nil {
		return rsi.ErrNotFound
	}
	h := t.runs.pluginHost(t.claim)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return err
	}
	if revision == "" {
		return rsi.ErrNotFound
	}
	input, _ := json.Marshal(contract.LimitsFacts{Role: role, Ceilings: codeLimitFacts(codemode.DefaultLimits())})
	raw, err := h.ReadService(ctx, revision, contract.LimitsContract, input, nil)
	if err != nil {
		return err
	}
	var plan contract.LimitsPlan
	if err = rsiplugin.DecodeResult(raw, &plan); err != nil {
		return rsi.ErrInvalid
	}
	chosen := codeRuntimeLimits(plan.Limits)
	if !validCodeLimits(chosen, codemode.DefaultLimits()) {
		return rsi.ErrInvalid
	}
	req.Limits = chosen
	return nil
}

func codeMethodUnavailable(err error) bool {
	return errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrInvalid) || errors.Is(err, rsi.ErrUnknown)
}

func decodeDiscoveryReply(raw string) (codemode.Reply, error) {
	var saved struct {
		Value json.RawMessage `json:"value,omitempty"`
		Error *tool.Error     `json:"error,omitempty"`
	}
	if err := rsiplugin.DecodeResult([]byte(raw), &saved); err != nil {
		return codemode.Reply{}, agent.ErrStorage
	}
	reply := codemode.Reply{Error: saved.Error}
	if len(saved.Value) > 0 {
		reply.Value = saved.Value
	}
	return reply, nil
}

// The original host.discover event is already accepted. A managed description
// freezes its complete reply before acknowledgment, including denials, so VM
// reconstruction cannot take another branch or reorder object keys on replay.
func (b *codeBridge) discoverVersioned(ctx context.Context, m codemode.Message) (codemode.Reply, error) {
	if m.ToolName == "searchTools" {
		return b.discoverSearchVersioned(ctx, m)
	}
	if m.ToolName != "describeTool" {
		return b.discover(m), nil
	}
	managed, err := b.t.runs.managedRunService(ctx, b.t.claim.RunID(), contract.FormatContract)
	if err != nil {
		return codemode.Reply{}, err
	}
	if !managed {
		return b.discover(m), nil
	}
	var name string
	if json.Unmarshal(m.Arguments, &name) != nil {
		return denied("INVALID_ARGUMENT"), nil
	}
	var spec *tool.Spec
	for _, s := range b.req.Tools {
		if s.Name == name && !codemode.ComputerBridge(s.Name) {
			copy := s
			spec = &copy
			break
		}
	}
	if spec == nil {
		return b.discover(m), nil
	}
	identity, err := encoded(*spec)
	if err != nil {
		return codemode.Reply{}, err
	}
	key := digest("steward.codemode.description/1:" + identity)
	cached := ""
	err = b.t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := b.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT reply_json FROM code_description_reply WHERE execution_id=? AND request_hash=?`, b.req.ExecutionID, key).Scan(&cached)
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
	if b.t.runs.plugins != nil {
		h := b.t.runs.pluginHost(b.t.claim)
		store := b.t.runs.host.Plugins()
		store.Fence = func(ctx context.Context, tx *sql.Tx) error { return b.fence(ctx, tx) }
		h.Store = store
		revision, err := h.Store.Binding(ctx, b.req.RunID)
		if err != nil && !codeMethodUnavailable(err) {
			return codemode.Reply{}, err
		}
		if err == nil && revision != "" {
			raw, err := h.ReadService(ctx, revision, contract.FormatContract, json.RawMessage(`{"input_id":"schema"}`), []rsi.InputData{{ID: "schema", Data: []byte(spec.InputSchema)}})
			if err == nil {
				var plan contract.FormatPlan
				if rsiplugin.DecodeResult(raw, &plan) == nil && validCodeFormat(plan) {
					reply = codemode.Reply{Value: codemode.DescribeInputType(*spec, plan.InputType, plan.Guidance)}
				}
			} else if !codeMethodUnavailable(err) {
				return codemode.Reply{}, err
			}
		}
	}
	raw, err := encoded(reply)
	if err != nil {
		return codemode.Reply{}, err
	}
	// A concurrent same-schema description returns the first accepted value.
	// Authority loss rejects all replies; quarantine can only accept a denial.
	err = b.t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := b.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT reply_json FROM code_description_reply WHERE execution_id=? AND request_hash=?`, b.req.ExecutionID, key).Scan(&cached)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if err = b.t.runs.pluginDispatchFence(ctx, tx, b.req.RunID); err != nil {
			if !errors.Is(err, rsi.ErrRevoked) {
				return err
			}
			raw, _ = encoded(denied("PROVIDER_UNAVAILABLE"))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO code_description_reply(execution_id,request_hash,reply_json) VALUES(?,?,?)`, b.req.ExecutionID, key, raw); err != nil {
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
func validCodeFormat(p contract.FormatPlan) bool {
	return strings.TrimSpace(p.InputType) != "" && len(p.InputType) <= 262144 && len(p.Guidance) <= 2048 && utf8.ValidString(p.InputType) && utf8.ValidString(p.Guidance) && !strings.ContainsRune(p.InputType, 0) && !strings.ContainsRune(p.Guidance, 0)
}
