package app

import (
	"context"

	"github.com/z-chenhao/assistant/internal/codingdraft"
)

// CodingDraft is an owner CLI operation. Model tools and plugins never receive a
// credential or an authority to select a remote repository and publish to it.
func (a *App) CodingDraft(ctx context.Context, operation, ref, approval string, target codingdraft.Target) (codingdraft.Receipt, error) {
	if operation == "draft-status" {
		return a.store.CodingDraft(ctx, ref, approval)
	}
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return codingdraft.Receipt{}, err
		}
	}
	remote := codingdraft.GitHub{Secrets: a.control.Secrets}
	switch operation {
	case "draft-plan":
		return a.store.PrepareCodingDraft(ctx, remote, ref, target)
	case "draft-approve":
		return a.store.ApproveCodingDraft(ctx, remote, ref, approval)
	case "draft-reconcile":
		return a.store.ReconcileCodingDraft(ctx, remote, ref, approval)
	}
	return codingdraft.Receipt{}, codingdraft.ErrInvalid
}
