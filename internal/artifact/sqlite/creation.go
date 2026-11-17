package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type creationFact struct {
	days        int
	graph, kind string
}

// ManagedOwnerTx is the actual installation boundary, not a test for whether
// the current producer happens to be present. Ever-activation dominates an old
// owner entitlement permanently. Product callers require a managed recipe on
// a fresh installation; native SDK fixtures remain explicitly distinguishable.
func (s *Store) ManagedOwnerTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var activated bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE json_extract(x.value,'$.contract')='steward.artifact-recipe/1')`).Scan(&activated)
	if err != nil || activated {
		return activated, err
	}
	if !s.RequireManaged {
		return false, nil
	}
	var legacy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_artifact_owner)`).Scan(&legacy); err != nil {
		return false, err
	}
	return !legacy, nil
}

// Storage consumes accepted facts only. It never executes a candidate in an
// Artifact transaction and cannot raise the Host's quota or size limits.
func (s *Store) creationTx(ctx context.Context, tx *sql.Tx, scope artifact.Scope, input artifact.Input) (creationFact, error) {
	if scope.RunID != "" {
		if input.Creation != nil {
			return creationFact{}, artifact.ErrInvalid
		}
		var managed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) m WHERE r.run_id=? AND m.value='steward.artifact-recipe/1')`, scope.RunID).Scan(&managed); err != nil {
			return creationFact{}, err
		}
		if !managed {
			return s.legacyCreation(), nil
		}
		var fact creationFact
		var kinds string
		err := tx.QueryRowContext(ctx, `SELECT p.retention_days,p.media_kinds_json,p.graph_revision FROM run_artifact_recipe p JOIN rsi_run r USING(run_id) WHERE p.run_id=? AND p.graph_revision=r.graph_revision`, scope.RunID).Scan(&fact.days, &kinds, &fact.graph)
		if errors.Is(err, sql.ErrNoRows) {
			return fact, artifact.ErrUnavailable
		}
		if err != nil {
			return fact, err
		}
		var mediaKinds []string
		if json.Unmarshal([]byte(kinds), &mediaKinds) != nil {
			return fact, artifact.ErrStorage
		}
		fact.kind = (artifact.Creation{MediaKinds: mediaKinds}).Kind(input.MediaType)
		return fact, nil
	}
	if input.Creation == nil {
		managed, err := s.ManagedOwnerTx(ctx, tx)
		if err != nil {
			return creationFact{}, err
		}
		if managed {
			return creationFact{}, artifact.ErrUnavailable
		}
		return s.legacyCreation(), nil
	}
	c := input.Creation
	if !scope.Owner || !c.Valid() || c.AgentID != scope.AgentID {
		return creationFact{}, artifact.ErrInvalid
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0)`, scope.AgentID).Scan(&epoch); err != nil {
		return creationFact{}, err
	}
	if epoch != c.PrivacyEpoch {
		return creationFact{}, artifact.ErrNotFound
	}
	var accepted, revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?),(SELECT stopped FROM rsi_control WHERE singleton=1) OR EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?)`, c.GraphRevision, c.GraphRevision).Scan(&accepted, &revoked); err != nil {
		return creationFact{}, err
	}
	if !accepted || revoked {
		return creationFact{}, artifact.ErrUnavailable
	}
	return creationFact{days: c.RetentionDays, graph: c.GraphRevision, kind: c.Kind(input.MediaType)}, nil
}
