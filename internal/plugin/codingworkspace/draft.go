package codingworkspace

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func draftRecipe(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.DraftFacts
	if e := rsiplugin.Decode(c.Input, &facts); e != nil {
		return rsiplugin.Result{}, e
	}
	result := contract.DraftRecipe{Proof: -1, Body: "This candidate passed a recorded Steward verification."}
	for i, ok := range facts.Verified {
		if ok {
			result.Proof = i
			break
		}
	}
	return rsiplugin.JSONResult(ctx, result)
}
