package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/connectedapps"
)

func readCardStates(ctx context.Context, state connectedapps.State, toolkits []string, selection connectedapps.DirectorySelection) ([]connectedapps.CardState, error) {
	projection, err := selection.Inventory(ctx, state)
	if err != nil {
		return nil, err
	}
	return projection.States(toolkits), nil
}
