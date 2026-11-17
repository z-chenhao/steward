package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
)

// WebWorkbenchSelection uses durable activation history, never catalog presence.
func (s *Store) WebWorkbenchSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Service)
}
