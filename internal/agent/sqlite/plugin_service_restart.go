package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
)

func (s *Store) ServiceRestartSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Service)
}
