package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

// Selection remembers prior activation: deactivation cannot restore native UI.
func (s *Store) ModelMenuSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Menu)
}

func (s *Store) ModelSettingsSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Settings)
}

func (s *Store) ModelConnectionSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.ConnectionPolicy)
}
