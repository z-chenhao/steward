package web

import (
	"context"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func (s *Server) inventoryCards(state connectedapps.State, selection connectedapps.DirectorySelection, indices []int) []appCard {
	var cards []appCard
	for _, index := range indices {
		row := state.Connections[index]
		cards = append(cards, s.cardFor(composio.Toolkit{Slug: row.Toolkit, Name: row.Name}, selection))
	}
	return cards
}
func finishInventory(view *appsView, cards []appCard) {
	view.Managed = cards
	if view.ConnectedOnly {
		view.Items = view.Managed
		view.Groups = groupApps(view.Items, "Your apps")
	}
}
func (s *Server) projectAppInventory(ctx context.Context, selection connectedapps.DirectorySelection, view *appsView) error {
	projection, err := selection.Inventory(ctx, view.State)
	if err != nil {
		return err
	}
	original := s.inventoryCards(view.State, selection, projection.Rows)
	order, err := selection.InventoryOrder(ctx, view.State.Revision, inventoryOrderFacts(original), view.ConnectedOnly)
	if err != nil {
		return err
	}
	finishInventory(view, projectInventoryOrder(original, order))
	cards := appViewCards(view)
	toolkits := make([]string, len(cards))
	for i, card := range cards {
		toolkits[i] = card.Slug
	}
	applyCardStates(cards, projection.States(toolkits))
	return nil
}

// This independent consumer remains available when directory metadata is disabled.
func (s *Server) projectAccountInventory(ctx context.Context, policy connectedapps.AccountInventoryPolicy, selection connectedapps.DirectorySelection, view *appsView) error {
	projection, err := policy.Project(ctx, view.State, view.ConnectedOnly)
	if err != nil {
		return err
	}
	finishInventory(view, s.inventoryCards(view.State, selection, projection.Rows))
	cards := appViewCards(view)
	toolkits := make([]string, len(cards))
	for i, card := range cards {
		toolkits[i] = card.Slug
	}
	applyCardStates(cards, projection.States(toolkits))
	return nil
}

func inventoryOrderFacts(cards []appCard) []contract.InventoryOrderFact {
	facts := make([]contract.InventoryOrderFact, len(cards))
	for i, card := range cards {
		facts[i] = contract.InventoryOrderFact{Name: card.Name, Slug: card.Slug}
	}
	return facts
}
func projectInventoryOrder(cards []appCard, order []int) []appCard {
	if len(order) == 0 {
		return nil
	}
	out := make([]appCard, len(order))
	for i, index := range order {
		out[i] = cards[index]
	}
	return out
}
