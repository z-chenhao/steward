package appsdirectory

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func captions(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts []contract.CaptionFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(facts) > contract.CaptionBatchSize {
		return rsiplugin.Result{}, errors.New("too many caption facts")
	}
	plans := make([]contract.CaptionPlan, len(facts))
	for i, f := range facts {
		if f.EditorialRunes < 0 || f.ProviderRunes < 0 {
			return rsiplugin.Result{}, errors.New("invalid caption facts")
		}
		plans[i] = chooseCaption(f)
	}
	return rsiplugin.JSONResult(ctx, plans)
}
func chooseCaption(f contract.CaptionFacts) contract.CaptionPlan {
	p := contract.CaptionPlan{Source: "editorial", End: f.EditorialRunes}
	if p.End == 0 {
		p.Source = "provider"
		p.End = f.ProviderRunes
	}
	if p.End == 0 {
		return contract.CaptionPlan{Source: "literal", Text: "Bring this app into your everyday work"}
	}
	if p.End > 150 {
		p.End = 147
		p.Text = "…"
	}
	return p
}
