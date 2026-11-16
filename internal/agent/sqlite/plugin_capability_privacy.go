package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/agent"
)

// MCP namespace and Apps guidance share only the original admission privacy
// ticket. No candidate IO may turn old ingress into a new privacy epoch.
type capabilityPrivacy struct {
	owner string
	epoch int64
}

func readCapabilityPrivacy(ctx context.Context, tx *sql.Tx) (capabilityPrivacy, error) {
	var p capabilityPrivacy
	err := tx.QueryRowContext(ctx, `SELECT a.agent_id,coalesce(p.epoch,0) FROM agent_config a LEFT JOIN memory_privacy p USING(agent_id) WHERE a.singleton=1`).Scan(&p.owner, &p.epoch)
	return p, err
}

func (p capabilityPrivacy) check(ctx context.Context, tx *sql.Tx) error {
	current, err := readCapabilityPrivacy(ctx, tx)
	if err != nil {
		return err
	}
	if current != p {
		return agent.ErrClaimLost
	}
	return nil
}
