package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/taskprogress/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The optional progress feature has no authority over Root work. Once managed,
// a missing selection suppresses projection; it never resurrects native policy.
func progressUnavailable(err error) bool {
	// An exact ErrUnknown means a prior process admitted a pure contribution
	// without completing it. Preserve that audit fact and omit this optional
	// contribution; never retry the unknown call or block business work.
	return err == rsi.ErrUnknown || errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked)
}

func (r *Runs) prepareProgressGuidance(ctx context.Context, c agent.RunClaim, run agent.Run) ([]agent.AgentMessage, error) {
	managed, err := r.managedRunService(ctx, run.ID, contract.GuidanceContract)
	if err != nil || !managed {
		return run.Checkpoint.Messages, err
	}
	var root bool
	if err = r.host.db.QueryRowContext(ctx, `SELECT worker_position='root' FROM run WHERE run_id=?`, run.ID).Scan(&root); err != nil || !root {
		return run.Checkpoint.Messages, err
	}
	if run.Checkpoint.ContextReady || run.Checkpoint.Step != 0 {
		return nil, agent.ErrConflict
	}
	// A historical context without the Host opt-in is never retroactively enabled.
	sections, ok := agent.FrozenSystemSections(run.Checkpoint.Messages)
	if !ok || len(sections) != 4 || !sections[3].Progress || sections[3].ChildText == "" {
		return run.Checkpoint.Messages, nil
	}
	instructions := ""
	if r.plugins != nil {
		h := r.pluginHost(c)
		revision, err := h.Store.Binding(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		if revision != "" {
			b, err := h.ServiceInstance(ctx, revision, contract.GuidanceContract)
			if err == nil {
				var raw []byte
				raw, err = h.Call(ctx, rsiplugin.Execution{RunID: run.ID}, b.InstanceID, contract.GuidanceContract, "task-progress:guidance", json.RawMessage(`{"role":"root"}`))
				if err == nil {
					var p contract.Guidance
					if err = rsiplugin.DecodeResult(raw, &p); err != nil {
						return nil, err
					}
					if strings.TrimSpace(p.Instructions) == "" {
						return nil, llm.ModelIncompatible
					}
					instructions = p.Instructions
				}
			}
			if err != nil && !progressUnavailable(err) {
				return nil, err
			}
		}
	}
	out, ok := agent.ApplyRootProgressGuidance(run.Checkpoint.Messages, instructions)
	if !ok {
		return nil, llm.ModelIncompatible
	}
	return out, nil
}

type progressSelection struct {
	managed, publish bool
	summary          string
}

func (r *Runs) prepareProgressSelection(ctx context.Context, c agent.RunClaim, cp agent.Checkpoint) (progressSelection, error) {
	p := progressSelection{summary: progressText(cp)}
	if p.summary == "" {
		return p, nil
	}
	var err error
	p.managed, err = r.managedRunService(ctx, c.RunID(), contract.SelectionContract)
	if err != nil || !p.managed {
		return p, err
	}
	eligible := false
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var err error
		eligible, err = progressEligible(ctx, tx, c.RunID())
		if err != nil || !eligible {
			return err
		}
		// Bind the selection to the complete, accepted model response. Arbitrary
		// checkpoint text cannot create another source or bypass Root opt-in.
		var source llm.GenerationResult
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT result_json FROM model_generation WHERE generation_id=? AND run_id=? AND step_index=? AND status='completed'`, cp.ExchangeID, c.RunID(), cp.Step).Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			return err
		}
		if source.Clipped || len(source.ToolCalls) == 0 || source.Text != cp.Result.Text {
			return agent.ErrConflict
		}
		return nil
	})
	if err != nil || !eligible || r.plugins == nil {
		return p, err
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, c.RunID())
	if err != nil || revision == "" {
		return p, err
	}
	b, err := h.ServiceInstance(ctx, revision, contract.SelectionContract)
	if progressUnavailable(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	input, _ := json.Marshal(contract.SelectionFacts{Summary: p.summary})
	raw, err := h.Call(ctx, rsiplugin.Execution{RunID: c.RunID()}, b.InstanceID, contract.SelectionContract, "task-progress:"+cp.ExchangeID, input)
	if progressUnavailable(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	var selection contract.Selection
	if err = rsiplugin.DecodeResult(raw, &selection); err != nil {
		return p, err
	}
	p.publish = selection.Publish
	return p, nil
}
