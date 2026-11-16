package app

import (
	"context"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction/web"
)

// RespondWebForm accepts ordinary user input. The client never supplies the
// prompt, labels, approval identities, target Run, or idempotency key.
func (a *App) RespondWebForm(ctx context.Context, messageID, formID string, values map[string]string) (agent.Receipt, error) {
	if err := a.CheckChannel(ctx, "web", web.Subject, web.Destination); err != nil {
		return agent.Receipt{}, err
	}
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		return agent.Receipt{}, err
	}
	m, err := a.history.Get(ctx, d.AgentID, messageID)
	if err != nil {
		return agent.Receipt{}, err
	}
	if m.Role != "assistant" {
		return agent.Receipt{}, conversation.ErrConflict
	}
	core := a.core.Load()
	status, err := a.Diagnostics(ctx)
	if err != nil || !status.AgentIngressEnabled || core.registry == nil || a.core.Load() != core {
		return agent.Receipt{}, agent.ErrSetup
	}
	return core.runs.AcceptWebForm(ctx, m, formID, values)
}
