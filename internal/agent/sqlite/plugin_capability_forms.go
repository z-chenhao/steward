package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
)

func (s *Store) CapabilityFormSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Service)
}

func (s *Store) CapabilityPresentationSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.PresentationService)
}
