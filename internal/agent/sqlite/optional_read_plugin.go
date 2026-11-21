package sqlite

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type optionalReadPlugin struct {
	instance, pluginID, contract, goal string
	cases                              []rsi.Case
}

// Installation is explicit, preserves the exact current bindings and imports
// the verification policy that actually authorized that graph's activation.
func (s *Store) enableOptionalReadPlugin(ctx context.Context, h rsi.Host, source string, service optionalReadPlugin) (rsi.Status, error) {
	status, err := h.Store.Status(ctx)
	if err != nil {
		return status, err
	}
	if status.Stopped || status.Revision == "" {
		return status, rsi.ErrRevoked
	}
	base, err := h.Store.Graph(ctx, status.Revision)
	if err != nil {
		return status, err
	}
	pkg, err := h.Install(ctx, source)
	if err != nil {
		return status, err
	}
	m := pkg.Manifest
	if m.PluginID != service.pluginID || len(m.Provides) != 1 || m.Provides[0].Contract != service.contract || !slices.Equal(m.Requests, []string{"input.read"}) || len(m.Requires)+len(m.Tools)+len(m.Hooks)+len(m.Resources) != 0 {
		return status, rsi.ErrInvalid
	}
	for _, b := range base.Graph.Bindings {
		if b.InstanceID == service.instance {
			if b.PackageDigest != pkg.Digest {
				return status, rsi.ErrConflict
			}
			return status, h.Store.CheckGraph(ctx, status.Revision)
		}
	}
	if _, err = h.ServiceInstance(ctx, status.Revision, service.contract); err == nil {
		return status, rsi.ErrConflict
	} else if !errors.Is(err, rsi.ErrNotFound) {
		return status, err
	}
	policyID, err := s.activeReadPolicyID(ctx, status)
	if err != nil {
		return status, err
	}
	policy, err := h.Store.Policy(ctx, policyID)
	if err != nil {
		return status, err
	}
	base.Graph.Bindings = append(base.Graph.Bindings, rsiplugin.Binding{InstanceID: service.instance, PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: service.instance, StateVersion: m.State.Version})
	g, err := h.Prepare(ctx, base.Graph)
	if err != nil {
		return status, err
	}
	suffix := fmt.Sprintf("%s-%d", strings.TrimPrefix(g.Revision, "sha256:")[:24], status.Epoch)
	policy.ID = service.instance + "-" + suffix
	policy.AllowedContracts = append(policy.AllowedContracts, service.contract)
	if !slices.Contains(policy.AllowedRequests, "input.read") {
		policy.AllowedRequests = append(policy.AllowedRequests, "input.read")
	}
	policy.Cases = append(policy.Cases, service.cases...)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		return status, err
	}
	c := rsi.Candidate{ID: service.instance + "-" + suffix, Baseline: status.Revision, Target: g.Revision, PolicyID: policy.ID, Goal: service.goal}
	if err = h.Propose(ctx, c); err != nil {
		return status, err
	}
	decision, err := h.Verify(ctx, c.ID)
	if err != nil {
		return status, err
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: c.ID, Expected: c.Baseline, Target: c.Target, DecisionID: decision}); err != nil {
		return status, err
	}
	return h.Store.Status(ctx)
}

func (s *Store) activeReadPolicyID(ctx context.Context, status rsi.Status) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.operation_id,json_extract(a.receipt_json,'$.previous_graph_revision'),a.request_hash,d.decision_id,c.policy_id
	  FROM rsi_activation a JOIN rsi_decision d ON d.target=json_extract(a.receipt_json,'$.graph_revision') AND d.passed=1 JOIN rsi_candidate c USING(candidate_id)
	  WHERE d.target=? AND json_extract(a.receipt_json,'$.epoch')=?`, status.Revision, status.Epoch)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		request := rsi.Activation{Target: status.Revision}
		var hash, id string
		if err = rows.Scan(&request.OperationID, &request.Expected, &hash, &request.DecisionID, &id); err != nil {
			return "", err
		}
		digest, err := rsiplugin.Digest("steward.rsi.activate/1", request)
		if err != nil {
			return "", err
		}
		if digest == hash {
			return id, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", rsi.ErrVerification
}
