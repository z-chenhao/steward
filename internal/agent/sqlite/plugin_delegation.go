package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/delegation/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type delegationPolicy struct {
	revision  string
	available bool
	contract.Policy
}

func validDelegationPolicy(p contract.Policy, c contract.Ceilings) bool {
	return p.Depth >= 0 && p.Depth <= c.Depth && p.Descendants >= 0 && p.Descendants <= c.Descendants && strings.TrimSpace(p.Instructions) != "" && len(p.Instructions) <= 4096 && utf8.ValidString(p.Instructions)
}

// Choose one pure recipe per new Root. The original run_tree safety ceilings
// remain immutable; this contribution can only narrow recursion and breadth.
// Child contexts inherit the accepted method, never another live plugin call.
func (r *Runs) prepareDelegation(ctx context.Context, c agent.RunClaim, run agent.Run) ([]agent.AgentMessage, *delegationPolicy, error) {
	messages := run.Checkpoint.Messages
	enabled := false
	for _, s := range run.Tools {
		enabled = enabled || (s.Name == agent.RunToolName && s.Permission == "spawn_agents")
	}
	if !enabled {
		return messages, nil, nil
	}
	managed, err := r.managedRunService(ctx, run.ID, contract.Contract)
	if err != nil || !managed {
		return messages, nil, err
	}
	var root bool
	if err = r.host.db.QueryRowContext(ctx, `SELECT worker_position='root' FROM run WHERE run_id=?`, run.ID).Scan(&root); err != nil || !root {
		return messages, nil, err
	}
	if run.Checkpoint.ContextReady || run.Checkpoint.Step != 0 {
		return nil, nil, agent.ErrConflict
	}
	var ceilings contract.Ceilings
	if err = r.host.db.QueryRowContext(ctx, `SELECT max_depth,max_descendants FROM run_tree WHERE root_run_id=?`, run.ID).Scan(&ceilings.Depth, &ceilings.Descendants); err != nil {
		return nil, nil, err
	}
	p := &delegationPolicy{Policy: contract.Policy{Instructions: "Delegation is unavailable for this run. Use available tools or report the missing capability."}}
	if r.plugins != nil {
		h := r.pluginHost(c)
		revision, err := h.Store.Binding(ctx, run.ID)
		if err != nil {
			return nil, nil, err
		}
		if revision != "" {
			input, _ := json.Marshal(ceilings)
			raw, err := h.ReadService(ctx, revision, contract.Contract, input, nil)
			if err == nil {
				if err = rsiplugin.DecodeResult(raw, &p.Policy); err != nil {
					return nil, nil, llm.ModelIncompatible
				}
				if !validDelegationPolicy(p.Policy, ceilings) {
					return nil, nil, llm.ModelIncompatible
				}
				p.revision, p.available = revision, true
			} else if errors.Is(err, rsi.ErrInvalid) {
				return nil, nil, llm.ModelIncompatible
			} else if !errors.Is(err, rsi.ErrNotFound) && !errors.Is(err, rsi.ErrRevoked) {
				return nil, nil, err
			}
		}
	}
	out, ok := agent.ApplyDelegationMethods(messages, run.Tools, p.Instructions)
	if !ok {
		return nil, nil, llm.ModelIncompatible
	}
	return out, p, nil
}

// Root method and initialized context commit together. No model, Child or Tool
// can run with an accepted context but a missing method record.
func (r *Runs) checkpointWorkerInitialization(ctx context.Context, c agent.RunClaim, run agent.Run, p *delegationPolicy) error {
	if p == nil {
		return r.Checkpoint(ctx, c, run.Version, run.Checkpoint)
	}
	if !run.Checkpoint.ContextReady || run.Checkpoint.Step != 0 || run.Checkpoint.Result != nil || run.Checkpoint.Phase != agent.ModelPhase {
		return agent.ErrConflict
	}
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := checkpoint(ctx, tx, c, run.Version, run.Checkpoint); err != nil {
			return err
		}
		var revision any
		if p.available {
			var frozen string
			if err := tx.QueryRowContext(ctx, `SELECT graph_revision FROM rsi_run WHERE run_id=?`, run.ID).Scan(&frozen); err != nil {
				return err
			}
			if frozen != p.revision {
				return agent.ErrConflict
			}
			if err := r.pluginDispatchFence(ctx, tx, run.ID); err != nil {
				return err
			}
			revision = p.revision
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO delegation_policy_binding(root_run_id,graph_revision,available,depth,descendants,instructions) VALUES(?,?,?,?,?,?)`, run.ID, revision, p.available, p.Depth, p.Descendants, p.Instructions)
		return err
	})
}
