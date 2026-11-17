package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
)

// Historical activation remains authoritative after deactivation or stop.
func (s *Store) FeishuStatusSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Status)
}

func (s *Store) FeishuCardSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Card)
}
