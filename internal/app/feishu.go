package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
)

func (a *App) FeishuStatus(ctx context.Context, check bool) feishu.ConnectionStatus {
	facts := feishu.StatusFacts{Channel: "feishu", Mode: "disabled", State: "not_configured"}
	if a.feishu != nil {
		if check {
			facts = a.feishu.CheckFacts(ctx)
		} else {
			facts = a.feishu.ConnectionFacts()
		}
	}
	out := feishu.ConnectionStatus{StatusFacts: facts}
	projected, managed, err := a.readFeishuStatus(ctx, contract.StatusFacts{State: facts.State})
	if err != nil {
		out.GuidanceUnavailable = true
		return out
	}
	if !managed {
		projected = legacyv1.ProjectStatus(contract.StatusFacts{State: facts.State})
	}
	out.Action = projected.Action
	return out
}
