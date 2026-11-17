package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
)

type guardedProvider struct {
	runs *Runs
	llm.LLMProvider
}

func (r *Runs) GuardProvider(p llm.LLMProvider) llm.LLMProvider { return guardedProvider{r, p} }
func (p guardedProvider) guarded(req llm.GenerationRequest) llm.GenerationRequest {
	req.DispatchGuard = func(ctx context.Context) (func(), error) {
		tx, e := p.runs.host.db.BeginTx(ctx, nil)
		if e != nil {
			return nil, agent.ErrStorage
		}
		var epoch int64
		e = tx.QueryRowContext(ctx, `SELECT epoch FROM run_claim WHERE run_id=? AND token=?`, req.RunID, req.OwnerToken).Scan(&epoch)
		if e == nil {
			e = fence(ctx, tx, &claim{id: req.RunID, token: req.OwnerToken, epoch: epoch})
		}
		if e == nil {
			e = p.runs.pluginDispatchFence(ctx, tx, req.RunID)
		}
		if e != nil {
			tx.Rollback()
			return nil, agent.ErrClaimLost
		}
		return func() { _ = tx.Rollback() }, nil
	}
	return req
}
func (p guardedProvider) Generate(ctx context.Context, r llm.GenerationRequest, s func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.LLMProvider.Generate(ctx, p.guarded(r), s)
}
func (p guardedProvider) Continue(ctx context.Context, r llm.GenerationRequest, s func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.LLMProvider.Continue(ctx, p.guarded(r), s)
}
