package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
)

func (s *Store) TerminalInterfaceSelection(ctx context.Context) (string, bool, error) {
	return s.ownerReadServiceSelection(ctx, contract.Service)
}
