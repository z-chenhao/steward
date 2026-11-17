package connectedapps

import (
	"context"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

// Exact pre-reconnect/default23/direct and already captured old interpretation.
// New managed errors never use this recipe. Delete with those real consumers.
func (s *Service) reconnectLegacyV1(ctx context.Context, c Connection) (composio.Link, error) {
	if err := s.disconnect(ctx, c.ID); err != nil {
		return composio.Link{}, err
	}
	return s.connect(ctx, c.Toolkit, c.Alias, "")
}
