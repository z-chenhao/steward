package connectedapps

import "context"

func readCardStates(ctx context.Context, state State, toolkits []string, selection DirectorySelection) ([]CardState, error) {
	projection, err := selection.Inventory(ctx, state)
	if err != nil {
		return nil, err
	}
	return projection.States(toolkits), nil
}
