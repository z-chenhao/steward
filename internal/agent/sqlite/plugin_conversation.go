package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/conversation/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (r *Runs) prepareConversation(ctx context.Context, c agent.RunClaim, run agent.Run) ([]agent.AgentMessage, error) {
	managed, err := r.managedRunService(ctx, run.ID, contract.Contract)
	if err != nil {
		return nil, err
	}
	if !managed {
		return run.Checkpoint.Messages, nil
	}
	if run.Kind != "main" || run.Checkpoint.ContextReady || run.Checkpoint.Step != 0 {
		return nil, llm.ModelIncompatible
	}
	if r.plugins == nil {
		return nil, llm.ProviderUnavailable
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return nil, llm.ProviderUnavailable
	}
	b, err := h.ServiceInstance(ctx, revision, contract.Contract)
	if errors.Is(err, rsi.ErrNotFound) {
		return nil, llm.ProviderUnavailable
	}
	if err != nil {
		return nil, err
	}
	raw, err := h.Call(ctx, rsiplugin.Execution{RunID: run.ID}, b.InstanceID, contract.Contract, "conversation-policy:main", json.RawMessage(`{"role":"main"}`))
	if errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrNotFound) {
		return nil, llm.ProviderUnavailable
	}
	if err != nil {
		return nil, err
	}
	var p contract.Policy
	if err = rsiplugin.DecodeResult(raw, &p); err != nil {
		return nil, err
	}
	out, ok := agent.ApplyMainMethods(run.Checkpoint.Messages, agent.MainMethods{Role: p.RolePolicy, Execution: p.ExecutionPolicy})
	if !ok {
		return nil, llm.ModelIncompatible
	}
	return out, nil
}
