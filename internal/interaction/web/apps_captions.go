package web

import (
	"context"
	"github.com/z-chenhao/assistant/internal/connectedapps"
)

// Cards already hold original facts. The same request's bound plugin chooses
// captions for every projection, including account and detail copies.
func projectAppCaptions(ctx context.Context, selection connectedapps.DirectorySelection, view *appsView) error {
	cards := appViewCards(view)
	sources := make([]connectedapps.CaptionSources, len(cards))
	for i, card := range cards {
		sources[i] = connectedapps.CaptionSources{Editorial: connectedapps.EditorialDescription(selection.Catalogue, card.Slug), Provider: card.Meta.Description}
	}
	captions, err := selection.ProjectCaptions(ctx, sources)
	if err != nil {
		return err
	}
	for i, card := range cards {
		card.Description = captions[i]
	}
	return nil
}

func appViewCards(view *appsView) []*appCard {
	var cards []*appCard
	for i := range view.Items {
		cards = append(cards, &view.Items[i])
	}
	for i := range view.Managed {
		cards = append(cards, &view.Managed[i])
	}
	for i := range view.Groups {
		for j := range view.Groups[i].Items {
			cards = append(cards, &view.Groups[i].Items[j])
		}
	}
	if view.Selected != nil {
		cards = append(cards, view.Selected)
	}

	return cards
}
