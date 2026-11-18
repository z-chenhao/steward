package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/taskmail/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Installation is explicit, preserves the exact current bindings and imports
// the verification policy that actually authorized that graph's activation.
func (s *Store) EnableTaskMail(ctx context.Context, h rsi.Host, source string) (rsi.Status, error) {
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
	if m.PluginID != contract.PluginID || len(m.Provides) != 1 || m.Provides[0].Contract != contract.Service || !slices.Equal(m.Requests, []string{"input.read"}) || len(m.Requires)+len(m.Tools)+len(m.Hooks)+len(m.Resources) != 0 {
		return status, rsi.ErrInvalid
	}
	for _, b := range base.Graph.Bindings {
		if b.InstanceID == "task-mail" {
			if b.PackageDigest != pkg.Digest {
				return status, rsi.ErrConflict
			}
			return status, h.Store.CheckGraph(ctx, status.Revision)
		}
	}
	if _, err = h.ServiceInstance(ctx, status.Revision, contract.Service); err == nil {
		return status, rsi.ErrConflict
	} else if !errors.Is(err, rsi.ErrNotFound) {
		return status, err
	}
	policyID, err := s.activeMailBasePolicyID(ctx, status)
	if err != nil {
		return status, err
	}
	policy, err := h.Store.Policy(ctx, policyID)
	if err != nil {
		return status, err
	}
	base.Graph.Bindings = append(base.Graph.Bindings, rsiplugin.Binding{InstanceID: "task-mail", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "task-mail", StateVersion: m.State.Version})
	g, err := h.Prepare(ctx, base.Graph)
	if err != nil {
		return status, err
	}
	suffix := fmt.Sprintf("%s-%d", strings.TrimPrefix(g.Revision, "sha256:")[:24], status.Epoch)
	policy.ID = "task-mail-" + suffix
	policy.AllowedContracts = append(policy.AllowedContracts, contract.Service)
	if !slices.Contains(policy.AllowedRequests, "input.read") {
		policy.AllowedRequests = append(policy.AllowedRequests, "input.read")
	}
	policy.Cases = append(policy.Cases, taskMailCases()...)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		return status, err
	}
	c := rsi.Candidate{ID: "mail-" + suffix, Baseline: status.Revision, Target: g.Revision, PolicyID: policy.ID, Goal: "Enable plain-text Task mail forwarding"}
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

func taskMailCases() []rsi.Case {
	makeCase := func(data, expected string) rsi.Case {
		return rsi.Case{InstanceID: "task-mail", Contract: contract.Service, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: "message", Data: []byte(data)}}, Expected: json.RawMessage(expected)}
	}
	return []rsi.Case{makeCase("From: test@example.org\r\nSubject: status\r\n\r\nreceived", `{"from":"test@example.org","subject":"status","text":"received"}`), makeCase("invalid", `{"error":"invalid_message"}`)}
}

func (s *Store) activeMailBasePolicyID(ctx context.Context, status rsi.Status) (string, error) {
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

func mailServiceAvailable(ctx context.Context, tx *sql.Tx) error {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_control c JOIN rsi_graph g ON g.revision=c.active_graph,
	json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
	json_each(p.manifest_json,'$.provides') x WHERE c.singleton=1 AND c.stopped=0 AND NOT EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=g.revision)
	AND json_extract(x.value,'$.contract')=? GROUP BY g.revision HAVING count(*)=1)`, contract.Service).Scan(&ok)
	if err == nil && !ok {
		return rsi.ErrRevoked
	}
	return err
}
