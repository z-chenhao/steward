package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ConfigureArtifactHost is composition-time only. Owner operations do not
// install a model, run Skills, or borrow a live execution claim.
func (s *Store) ConfigureArtifactHost(catalog rsi.Catalog) {
	s.pluginCatalog.Store(&catalog)
	s.Artifacts().RequireManaged = true
}

func (s *Store) artifactOwnerFence(ctx context.Context, tx *sql.Tx, agentID string, epoch int64) error {
	var current string
	var privacy int64
	if err := tx.QueryRowContext(ctx, `SELECT agent_id,coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=agent_config.agent_id),0) FROM agent_config WHERE singleton=1`).Scan(&current, &privacy); err != nil {
		return err
	}
	if current != agentID || privacy != epoch {
		return agent.ErrClaimLost
	}
	return nil
}

// PrepareArtifactInput resolves only the private creation policy. Staging and
// metadata commit retain their original byte/lock/quota/authorization pipeline;
// commit rechecks the same accepted graph and privacy fact after any long I/O.
func (s *Store) PrepareArtifactInput(ctx context.Context, input artifact.Input) (artifact.Input, error) {
	if input.Creation != nil {
		return artifact.Input{}, artifact.ErrInvalid
	}
	host, access, managed, err := s.artifactOwnerPolicyHost(ctx)
	if err != nil {
		return artifact.Input{}, err
	}
	if !managed {
		return input, nil
	}
	explicit := 0
	if choice := s.Artifacts().RetentionDays; choice != nil {
		explicit = *choice
	}
	raw, err := json.Marshal(contract.RecipeRequest{RetentionDays: explicit})
	if err != nil {
		return artifact.Input{}, err
	}
	response, err := host.ReadService(ctx, access.revision, contract.Recipe, raw, nil)
	if err != nil {
		if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrInvalid) {
			return artifact.Input{}, artifact.ErrUnavailable
		}
		return artifact.Input{}, err
	}
	var plan contract.RecipePlan
	if err := rsiplugin.DecodeResult(response, &plan); err != nil {
		return artifact.Input{}, err
	}
	if !validArtifactRecipe(plan, explicit) {
		return artifact.Input{}, artifact.ErrUnavailable
	}
	input.Creation = &artifact.Creation{GraphRevision: access.revision, RetentionDays: plan.RetentionDays, MediaKinds: append([]string{}, plan.MediaKinds...), AgentID: access.scope.AgentID, PrivacyEpoch: access.epoch}
	return input, nil
}

type artifactOwnerAccess struct {
	scope    artifact.Scope
	revision string
	epoch    int64
}

func (s *Store) artifactOwnerPolicyHost(ctx context.Context) (rsi.Host, artifactOwnerAccess, bool, error) {
	var access artifactOwnerAccess
	access.scope = artifact.Scope{UserID: s.OwnerID(), Owner: true}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rsi.Host{}, access, false, err
	}
	managed, err := s.Artifacts().ManagedOwnerTx(ctx, tx)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT a.agent_id,coalesce(c.active_graph,''),coalesce(p.epoch,0) FROM agent_config a JOIN rsi_control c ON c.singleton=1 LEFT JOIN memory_privacy p USING(agent_id) WHERE a.singleton=1`).Scan(&access.scope.AgentID, &access.revision, &access.epoch)
	}
	tx.Rollback()
	if err != nil || !managed {
		return rsi.Host{}, access, managed, err
	}
	if access.revision == "" {
		return rsi.Host{}, access, true, artifact.ErrUnavailable
	}
	catalog := rsi.NewCatalog(s.DataRoot())
	if configured := s.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	control := s.Plugins()
	control.Fence = func(ctx context.Context, tx *sql.Tx) error {
		return s.artifactOwnerFence(ctx, tx, access.scope.AgentID, access.epoch)
	}
	return rsi.Host{Catalog: catalog, Store: control}, access, true, nil
}
