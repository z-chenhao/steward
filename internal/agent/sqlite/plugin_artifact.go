package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Real accepted search calls use the Run's frozen producer. No SQL transaction
// survives across candidate execution or immutable-input callbacks, and no
// native branch handles a missing, failed, quarantined or disabled producer.
func (t *tools) searchArtifactPlugin(ctx context.Context, invocation tool.Invocation) (tool.Result, bool, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Search)
	if err != nil || !managed {
		return tool.Result{}, false, err
	}
	var request contract.SearchRequest
	handled := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		_, name, args, err := t.acceptedBuiltin(ctx, tx, invocation)
		if err != nil {
			return err
		}
		if name != artifacttools.Search {
			return nil
		}
		handled = true
		return json.Unmarshal([]byte(args), &request)
	})
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), handled, nil
	}
	if err != nil || !handled {
		return tool.Result{}, handled, err
	}
	host, access, err := t.pureToolPolicyHost(ctx, invocation)
	if err != nil {
		result, e := artifactPolicyFailure(err)
		return result, true, e
	}
	if err := host.Store.CheckGraph(ctx, access.pluginGraph); err != nil {
		result, e := artifactPolicyFailure(err)
		return result, true, e
	}
	if _, err := host.ServiceInstance(ctx, access.pluginGraph, contract.Search); err != nil {
		result, e := artifactPolicyFailure(err)
		return result, true, e
	}
	type candidateIdentity struct {
		ref, hash string
		version   int64
	}
	var candidates []candidateIdentity
	var metadata []byte
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, access); err != nil {
			return err
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		return t.runs.host.Artifacts().VisitSearchCandidatesTx(ctx, tx, scope, func(c artifactsqlite.SearchCandidate) {
			candidates = append(candidates, candidateIdentity{c.Ref, c.ContentHash, c.Version})
			metadata = contract.AppendSearchRow(metadata, contract.SearchRow{CreatedAt: c.CreatedAt, Name: c.Name, MediaType: c.MediaType})
		})
	})
	if err != nil {
		return tool.Result{}, true, err
	}
	request.Count = len(candidates)
	input, err := json.Marshal(request)
	if err != nil {
		return tool.Result{}, true, err
	}
	response, err := host.ReadServiceStream(ctx, access.pluginGraph, contract.Search, input, rsi.InputData{ID: contract.SearchInput, Data: metadata})
	if err != nil {
		result, e := artifactPolicyFailure(err)
		return result, true, e
	}
	var plan contract.Selection
	if err := rsiplugin.DecodeResult(response, &plan); err != nil {
		return tool.Result{}, true, err
	}
	if plan.ErrorCode == "INVALID_ARGUMENT" && len(plan.Indices) == 0 && plan.Limit == 0 {
		return tool.Failure("INVALID_ARGUMENT"), true, nil
	}
	if plan.ErrorCode != "" || plan.Limit < 1 || plan.Limit > 50 || len(plan.Indices) > plan.Limit || (request.Limit > 0 && plan.Limit > request.Limit) {
		return tool.Result{}, true, rsi.ErrInvalid
	}
	selected := make([]artifact.Descriptor, 0, len(plan.Indices))
	seen := map[int]bool{}
	for _, index := range plan.Indices {
		if index < 0 || index >= len(candidates) || seen[index] {
			return tool.Result{}, true, rsi.ErrInvalid
		}
		seen[index] = true

	}
	// Only Host facts are projected. Recheck the original authorization and live
	// metadata after the untrusted calculation, including empty-result reads.
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, access); err != nil {
			return err
		}
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, invocation); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		for _, index := range plan.Indices {
			value := candidates[index]
			fresh, err := t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, value.ref, true)
			if err != nil {
				return err
			}
			if fresh.Version != value.version || fresh.ContentHash != value.hash {
				return artifact.ErrConflict
			}
			selected = append(selected, fresh)
		}
		return nil
	})
	if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrConflict) {
		return artifactFailure(err), true, nil
	}
	if err != nil {
		result, e := artifactPolicyFailure(err)
		return result, true, e
	}
	raw, err := encoded(selected)
	return tool.Result{Text: raw}, true, err
}

func artifactPolicyFailure(err error) (tool.Result, error) {
	if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return tool.Result{}, err
	}
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrInvalid) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	return tool.Result{}, err
}

func (t *tools) artifactReadPolicy(ctx context.Context, invocation tool.Invocation, request artifactReadRequest, descriptor artifact.Descriptor) (contract.ReadPlan, bool, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Read)
	if err != nil {
		return contract.ReadPlan{}, managed, err
	}
	if !managed {
		return legacyArtifactReadPolicy(request, descriptor), false, nil
	}
	host, access, err := t.pureToolPolicyHost(ctx, invocation)
	if err != nil {
		return contract.ReadPlan{}, true, err
	}
	input, err := json.Marshal(contract.ReadRequest{MediaType: descriptor.MediaType, ByteSize: descriptor.ByteSize, Offset: request.Offset, Limit: request.Limit})
	if err != nil {
		return contract.ReadPlan{}, true, err
	}
	raw, err := host.ReadService(ctx, access.pluginGraph, contract.Read, input, nil)
	if err != nil {
		return contract.ReadPlan{}, true, err
	}
	var plan contract.ReadPlan
	if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
		return contract.ReadPlan{}, true, err
	}
	return plan, true, nil
}

// The open file descriptor's exact bytes/hash were verified before policy
// execution. Current grants, liveness, privacy and Guardian still govern the
// eventual release, including binary-reference results.
func (t *tools) recheckArtifactRead(ctx context.Context, invocation tool.Invocation, original artifact.Descriptor, result tool.Result) (tool.Result, error) {
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, invocation); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		fresh, err := t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, original.Ref, true)
		if err != nil {
			return err
		}
		if fresh.Version != original.Version || fresh.ContentHash != original.ContentHash {
			return artifact.ErrIntegrity
		}
		return nil
	})
	if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrIntegrity) {
		return artifactFailure(err), nil
	}
	if err != nil {
		return artifactPolicyFailure(err)
	}
	return result, nil
}
