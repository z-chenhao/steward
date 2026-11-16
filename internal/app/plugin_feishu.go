package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"unicode/utf8"
)

func (a *App) readFeishuStatus(ctx context.Context, facts contract.StatusFacts) (contract.StatusProjection, bool, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.StatusProjection{}, true, err
		}
	}
	if a.store == nil {
		return contract.StatusProjection{}, false, nil
	}
	return a.readFeishuStatusWithHost(ctx, a.Plugins(), facts)
}
func (a *App) readFeishuStatusWithHost(ctx context.Context, host rsi.Host, facts contract.StatusFacts) (contract.StatusProjection, bool, error) {
	// Storeless historical diagnostic embeddings have never selected a service.
	if a.store == nil {
		return contract.StatusProjection{}, false, nil
	}
	revision, managed, err := a.store.FeishuStatusSelection(ctx)
	if err != nil || !managed {
		return contract.StatusProjection{}, managed, err
	}
	if revision == "" {
		return contract.StatusProjection{}, true, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.StatusProjection{}, true, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Status, input, nil)
	if err != nil {
		return contract.StatusProjection{}, true, err
	}
	var out contract.StatusProjection
	if err = rsiplugin.DecodeResult(raw, &out); err != nil {
		return out, true, err
	}
	if !utf8.ValidString(out.Action) || len(out.Action) > 4096 || strings.ContainsRune(out.Action, 0) {
		return contract.StatusProjection{}, true, rsi.ErrInvalid
	}
	return out, true, nil
}

func (a *App) readFeishuCard(ctx context.Context, facts contract.CardFacts) (contract.CardProjection, error) {
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return contract.CardProjection{}, err
		}
	}
	if a.store == nil {
		return legacyv1.ProjectCard(facts), nil
	}
	return a.readFeishuCardWithHost(ctx, a.Plugins(), facts)
}
func (a *App) readFeishuCardWithHost(ctx context.Context, host rsi.Host, facts contract.CardFacts) (contract.CardProjection, error) {
	if a.store == nil {
		return legacyv1.ProjectCard(facts), nil
	}
	revision, managed, err := a.store.FeishuCardSelection(ctx)
	if err != nil {
		return contract.CardProjection{}, err
	}
	if !managed {
		return legacyv1.ProjectCard(facts), nil
	}
	if revision == "" {
		return contract.CardProjection{}, rsi.ErrNotFound
	}
	input, err := json.Marshal(facts)
	if err != nil {
		return contract.CardProjection{}, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Card, input, nil)
	if err != nil {
		return contract.CardProjection{}, err
	}
	var out contract.CardProjection
	err = rsiplugin.DecodeResult(raw, &out)
	return out, err
}
