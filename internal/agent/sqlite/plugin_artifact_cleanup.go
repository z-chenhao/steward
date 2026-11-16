package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (s *Store) CleanupArtifacts(ctx context.Context, dry bool) (artifact.CleanupReport, error) {
	report := artifact.CleanupReport{DryRun: dry, Artifacts: []string{}}
	host, access, managed, err := s.artifactOwnerPolicyHost(ctx)
	if err != nil {
		return report, err
	}
	if !managed {
		return s.Artifacts().CleanupLegacy(ctx, access.scope, dry)
	}
	if err := host.Store.CheckGraph(ctx, access.revision); err != nil {
		return report, err
	}
	if _, err := host.ServiceInstance(ctx, access.revision, contract.Cleanup); err != nil {
		return report, artifact.ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	var candidates []artifactsqlite.CleanupCandidate
	if err = s.artifactOwnerFence(ctx, tx, access.scope.AgentID, access.epoch); err == nil {
		candidates, err = s.Artifacts().CleanupCandidatesTx(ctx, tx, access.scope)
	}
	tx.Rollback()
	if err != nil {
		return report, err
	}
	rows := make([]contract.CleanupRow, len(candidates))
	for i, value := range candidates {
		rows[i] = contract.CleanupRow{ExpiresAt: value.ExpiresAt}
	}
	batch := 0
	if choice := s.Artifacts().CleanupBatch; choice != nil {
		batch = *choice
	}
	request, err := json.Marshal(contract.CleanupRequest{Count: len(rows), Batch: batch})
	if err != nil {
		return report, err
	}
	raw, err := host.ReadServiceStream(ctx, access.revision, contract.Cleanup, request, rsi.InputData{ID: contract.CleanupInput, Data: contract.EncodeCleanup(rows)})
	if err != nil {
		return report, err
	}
	var plan contract.Selection
	if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
		return report, err
	}
	if plan.ErrorCode != "" || plan.Limit < 1 || plan.Limit > 10000 || batch > 0 && plan.Limit != batch || len(plan.Indices) > plan.Limit {
		return report, artifact.ErrUnavailable
	}
	selected := make([]artifactsqlite.CleanupCandidate, 0, len(plan.Indices))
	seen := map[int]bool{}
	for _, index := range plan.Indices {
		if index < 0 || index >= len(candidates) || seen[index] {
			return report, artifact.ErrUnavailable
		}
		seen[index] = true
		selected = append(selected, candidates[index])
	}
	s.boundary("before_artifact_cleanup_commit")
	return s.Artifacts().ApplyCleanup(ctx, access.scope, selected, dry, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.artifactOwnerFence(ctx, tx, access.scope.AgentID, access.epoch); err != nil {
			return err
		}
		var accepted, revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?),(SELECT stopped FROM rsi_control WHERE singleton=1) OR EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?)`, access.revision, access.revision).Scan(&accepted, &revoked); err != nil {
			return err
		}
		if !accepted || revoked {
			return artifact.ErrUnavailable
		}
		return nil
	})
}
