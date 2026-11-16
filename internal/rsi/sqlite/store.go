// Package sqlite persists RSI authority on the Host's existing state connection.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type Store struct {
	DB *sql.DB
	// Fence is supplied by the executing domain, never by an RPC caller.
	Fence func(context.Context, *sql.Tx) error
}

func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.Fence != nil {
		if err = s.Fence(ctx, tx); err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func raw(v any) string { b, _ := json.Marshal(v); return string(b) }
func audit(ctx context.Context, tx *sql.Tx, kind, ref string, v any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO rsi_audit(kind,reference,detail_json) VALUES(?,?,?)`, kind, ref, raw(v))
	return err
}
func (s *Store) Install(ctx context.Context, p rsi.Package) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var digest string
		err := tx.QueryRowContext(ctx, `SELECT digest FROM rsi_package WHERE plugin_id=? AND version=?`, p.Manifest.PluginID, p.Manifest.Version).Scan(&digest)
		if err == nil {
			if digest != p.Digest {
				return rsi.ErrConflict
			}
			res, e := tx.ExecContext(ctx, `DELETE FROM rsi_retired WHERE digest=?`, p.Digest)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return nil
			}
			return audit(ctx, tx, "package.reinstalled", p.Digest, struct{}{})
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_package VALUES(?,?,?,?)`, p.Digest, p.Manifest.PluginID, p.Manifest.Version, raw(p.Manifest)); err != nil {
			return err
		}
		return audit(ctx, tx, "package.installed", p.Digest, struct{ Plugin, Version string }{p.Manifest.PluginID, p.Manifest.Version})
	})
}
func (s *Store) PutGraph(ctx context.Context, g rsi.ResolvedGraph) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		for _, b := range g.Graph.Bindings {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_package WHERE digest=? AND NOT EXISTS(SELECT 1 FROM rsi_retired WHERE digest=rsi_package.digest))`, b.PackageDigest).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return rsi.ErrNotFound
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO rsi_graph VALUES(?,?,?) ON CONFLICT DO NOTHING`, g.Revision, raw(g.Graph), raw(g.Order))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		return audit(ctx, tx, "graph.prepared", g.Revision, struct{ Bindings int }{len(g.Graph.Bindings)})
	})
}
func (s *Store) Graph(ctx context.Context, id string) (rsi.ResolvedGraph, error) {
	out := rsi.ResolvedGraph{Revision: id}
	var graph, order string
	err := s.DB.QueryRowContext(ctx, `SELECT graph_json,order_json FROM rsi_graph WHERE revision=?`, id).Scan(&graph, &order)
	if err == sql.ErrNoRows {
		return out, rsi.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(graph), &out.Graph) != nil || json.Unmarshal([]byte(order), &out.Order) != nil {
		return out, rsi.ErrInvalid
	}
	return out, nil
}
func (s *Store) PutPolicy(ctx context.Context, p rsi.Policy) error {
	if err := rsi.ValidatePolicy(p); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT policy_json FROM rsi_policy WHERE policy_id=?`, p.ID).Scan(&old)
		if err == nil {
			if old != raw(p) {
				return rsi.ErrConflict
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_policy VALUES(?,?)`, p.ID, raw(p)); err != nil {
			return err
		}
		return audit(ctx, tx, "policy.created", p.ID, struct{}{})
	})
}
func (s *Store) Policy(ctx context.Context, id string) (rsi.Policy, error) {
	var out rsi.Policy
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT policy_json FROM rsi_policy WHERE policy_id=?`, id).Scan(&value)
	if err == sql.ErrNoRows {
		return out, rsi.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(value), &out)
	}
	return out, err
}
func (s *Store) Propose(ctx context.Context, c rsi.Candidate) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var active string
		var stopped bool
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,''),stopped FROM rsi_control WHERE singleton=1`).Scan(&active, &stopped); err != nil {
			return err
		}
		if stopped {
			return rsi.ErrRevoked
		}
		if active != c.Baseline {
			return rsi.ErrConflict
		}
		var old string
		err := tx.QueryRowContext(ctx, `SELECT candidate_json FROM rsi_candidate WHERE candidate_id=?`, c.ID).Scan(&old)
		if err == nil {
			if old != raw(c) {
				return rsi.ErrConflict
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_candidate VALUES(?,?,?,?)`, c.ID, raw(c), c.Target, c.PolicyID); err != nil {
			return err
		}
		return audit(ctx, tx, "candidate.proposed", c.ID, struct{ Target string }{c.Target})
	})
}
func (s *Store) Candidate(ctx context.Context, id string) (rsi.Candidate, error) {
	var c rsi.Candidate
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT candidate_json FROM rsi_candidate WHERE candidate_id=?`, id).Scan(&v)
	if err == sql.ErrNoRows {
		return c, rsi.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(v), &c)
	}
	return c, err
}
func (s *Store) Decide(ctx context.Context, e rsi.Evidence) (string, error) {
	id, err := rsiplugin.Digest("steward.rsi.decision/1", e)
	if err != nil {
		return "", err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var candidate, policy string
		if err := tx.QueryRowContext(ctx, `SELECT c.candidate_json,p.policy_json FROM rsi_candidate c JOIN rsi_policy p USING(policy_id) WHERE c.candidate_id=?`, e.CandidateID).Scan(&candidate, &policy); err != nil {
			return err
		}
		var c rsi.Candidate
		var p rsi.Policy
		if json.Unmarshal([]byte(candidate), &c) != nil || json.Unmarshal([]byte(policy), &p) != nil {
			return rsi.ErrInvalid
		}
		hash, _ := rsiplugin.Digest("steward.rsi.policy/1", p)
		if c.Target != e.Target || hash != e.PolicyDigest {
			return rsi.ErrConflict
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO rsi_decision VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, id, e.CandidateID, e.Target, e.Passed, raw(e))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		return audit(ctx, tx, "candidate.decided", id, e)
	})
	return id, err
}

func (s *Store) Decision(ctx context.Context, id string) (rsi.Evidence, error) {
	var out rsi.Evidence
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT evidence_json FROM rsi_decision WHERE decision_id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, rsi.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &out)
	}
	return out, err
}
func (s *Store) Activate(ctx context.Context, a rsi.Activation) (rsi.Receipt, error) {
	var out rsi.Receipt
	if a.OperationID == "" || len(a.OperationID) > 256 {
		return out, rsi.ErrInvalid
	}
	hash, err := rsiplugin.Digest("steward.rsi.activate/1", a)
	if err != nil {
		return out, err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var oldHash, receipt string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM rsi_activation WHERE operation_id=?`, a.OperationID).Scan(&oldHash, &receipt)
		if err == nil {
			if oldHash != hash {
				return rsi.ErrConflict
			}
			return json.Unmarshal([]byte(receipt), &out)
		}
		if err != sql.ErrNoRows {
			return err
		}
		var current string
		var epoch int64
		var stopped bool
		if err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,''),epoch,stopped FROM rsi_control WHERE singleton=1`).Scan(&current, &epoch, &stopped); err != nil {
			return err
		}
		if stopped {
			return rsi.ErrRevoked
		}
		if current != a.Expected {
			return rsi.ErrConflict
		}
		var target string
		var passed bool
		var candidateJSON string
		if err = tx.QueryRowContext(ctx, `SELECT d.target,d.passed,c.candidate_json FROM rsi_decision d JOIN rsi_candidate c USING(candidate_id) WHERE d.decision_id=?`, a.DecisionID).Scan(&target, &passed, &candidateJSON); err != nil {
			return rsi.ErrVerification
		}
		var candidate rsi.Candidate
		if json.Unmarshal([]byte(candidateJSON), &candidate) != nil || !passed || target != a.Target || candidate.Baseline != a.Expected {
			return rsi.ErrVerification
		}
		var quarantined bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?)`, target).Scan(&quarantined); err != nil {
			return err
		}
		if quarantined {
			return rsi.ErrRevoked
		}
		var graphJSON string
		if err = tx.QueryRowContext(ctx, `SELECT graph_json FROM rsi_graph WHERE revision=?`, target).Scan(&graphJSON); err != nil {
			return err
		}
		var graph rsiplugin.Graph
		if json.Unmarshal([]byte(graphJSON), &graph) != nil {
			return rsi.ErrInvalid
		}
		for _, b := range graph.Bindings {
			var manifestJSON string
			if err = tx.QueryRowContext(ctx, `SELECT manifest_json FROM rsi_package WHERE digest=? AND NOT EXISTS(SELECT 1 FROM rsi_retired WHERE digest=rsi_package.digest)`, b.PackageDigest).Scan(&manifestJSON); err != nil {
				return err
			}
			var m rsiplugin.Manifest
			if json.Unmarshal([]byte(manifestJSON), &m) != nil {
				return rsi.ErrInvalid
			}

		}
		res, err := tx.ExecContext(ctx, `UPDATE rsi_control SET active_graph=?,epoch=epoch+1 WHERE singleton=1 AND epoch=? AND coalesce(active_graph,'')=? AND stopped=0`, target, epoch, a.Expected)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return rsi.ErrConflict
		}
		out = rsi.Receipt{OperationID: a.OperationID, Previous: current, Current: target, Epoch: epoch + 1}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_activation VALUES(?,?,?)`, a.OperationID, hash, raw(out)); err != nil {
			return err
		}
		return audit(ctx, tx, "graph.activated", a.OperationID, out)
	})
	return out, err
}

func (s *Store) ActivationReceipt(ctx context.Context, a rsi.Activation) (rsi.Receipt, error) {
	var out rsi.Receipt
	hash, err := rsiplugin.Digest("steward.rsi.activate/1", a)
	if err != nil {
		return out, err
	}
	var old, value string
	err = s.DB.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM rsi_activation WHERE operation_id=?`, a.OperationID).Scan(&old, &value)
	if err == sql.ErrNoRows {
		return out, rsi.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if old != hash {
		return out, rsi.ErrConflict
	}
	return out, json.Unmarshal([]byte(value), &out)
}
func (s *Store) Status(ctx context.Context) (rsi.Status, error) {
	var out rsi.Status
	err := s.DB.QueryRowContext(ctx, `SELECT coalesce(active_graph,''),epoch,stopped FROM rsi_control WHERE singleton=1`).Scan(&out.Revision, &out.Epoch, &out.Stopped)
	return out, err
}
func (s *Store) Stop(ctx context.Context, stop bool) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE rsi_control SET stopped=?,epoch=epoch+1 WHERE singleton=1`, stop); err != nil {
			return err
		}
		return audit(ctx, tx, "control.stopped", "", struct{ Stopped bool }{stop})
	})
}
func (s *Store) Quarantine(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO rsi_quarantine VALUES(?) ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
		return audit(ctx, tx, "graph.quarantined", id, struct{}{})
	})
}
func (s *Store) Audit(ctx context.Context) ([]rsi.Audit, error) {
	out := []rsi.Audit{}
	rows, err := s.DB.QueryContext(ctx, `SELECT sequence,kind,reference,detail_json FROM rsi_audit ORDER BY sequence DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a rsi.Audit
		var value string
		if err = rows.Scan(&a.Sequence, &a.Kind, &a.Reference, &value); err != nil {
			return nil, err
		}
		a.Detail = json.RawMessage(value)
		out = append(out, a)
	}
	return out, rows.Err()
}

func binding(ctx context.Context, tx *sql.Tx, run string) (string, error) {
	var revision string
	var stopped, bad bool
	err := tx.QueryRowContext(ctx, `SELECT coalesce(b.graph_revision,''),c.stopped,EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=b.graph_revision) FROM rsi_run b JOIN rsi_control c ON c.singleton=1 WHERE b.run_id=?`, run).Scan(&revision, &stopped, &bad)
	if err == sql.ErrNoRows {
		return "", rsi.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if revision != "" && (stopped || bad) {
		return "", rsi.ErrRevoked
	}
	return revision, nil
}
func (s *Store) Binding(ctx context.Context, run string) (string, error) {
	var out string
	err := s.write(ctx, func(tx *sql.Tx) error { var err error; out, err = binding(ctx, tx, run); return err })
	return out, err
}

// CheckGraph is also used by explicitly authorized owner reads without a Run.
// The domain's supplied Fence is rechecked before any input chunk is released.
func (s *Store) CheckGraph(ctx context.Context, revision string) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.Fence != nil {
		if err = s.Fence(ctx, tx); err != nil {
			return err
		}
	}
	var exists, blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_graph g WHERE revision=? AND EXISTS(SELECT 1 FROM rsi_activation a WHERE json_extract(a.receipt_json,'$.graph_revision')=g.revision)),(SELECT stopped FROM rsi_control WHERE singleton=1) OR EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?)`, revision, revision).Scan(&exists, &blocked); err != nil {
		return err
	}
	if !exists {
		return rsi.ErrNotFound
	}
	if blocked {
		return rsi.ErrRevoked
	}
	return nil
}
func stateFor(ctx context.Context, tx *sql.Tx, run, instance string) (string, rsiplugin.State, error) {
	var state rsiplugin.State
	revision, err := binding(ctx, tx, run)
	if err != nil {
		return "", state, err
	}
	var graphJSON, agent string
	if err = tx.QueryRowContext(ctx, `SELECT g.graph_json,r.agent_id FROM rsi_graph g JOIN rsi_run b ON b.graph_revision=g.revision JOIN run r ON r.run_id=b.run_id WHERE b.run_id=? AND g.revision=?`, run, revision).Scan(&graphJSON, &agent); err != nil {
		return "", state, err
	}
	var graph rsiplugin.Graph
	if json.Unmarshal([]byte(graphJSON), &graph) != nil {
		return "", state, rsi.ErrInvalid
	}
	for _, b := range graph.Bindings {
		if b.InstanceID != instance {
			continue
		}
		var m rsiplugin.Manifest
		var manifest string
		if err = tx.QueryRowContext(ctx, `SELECT manifest_json FROM rsi_package WHERE digest=?`, b.PackageDigest).Scan(&manifest); err != nil {
			return "", state, err
		}
		if json.Unmarshal([]byte(manifest), &m) != nil {
			return "", state, rsi.ErrInvalid
		}
		namespace := run + ":" + b.StateNamespace
		hash, _ := rsiplugin.Digest("steward.rsi.schema/1", m.State.Schema)
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_state VALUES(?,?,?,?,0,?) ON CONFLICT DO NOTHING`, namespace, m.PluginID, b.StateVersion, hash, string(m.State.Initial)); err != nil {
			return "", state, err
		}
		var owner, schema, value string
		var version int
		if err = tx.QueryRowContext(ctx, `SELECT plugin_id,format_version,schema_hash,version,value_json FROM rsi_state WHERE namespace=?`, namespace).Scan(&owner, &version, &schema, &state.Version, &value); err != nil {
			return "", state, err
		}
		if owner != m.PluginID || version != b.StateVersion || schema != hash {
			return "", state, rsi.ErrConflict
		}
		state.Value = json.RawMessage(value)
		return namespace, state, nil
	}
	return "", state, rsi.ErrNotFound
}
func (s *Store) BeginCall(ctx context.Context, run, instance, key, hash string) (json.RawMessage, *rsiplugin.State, error) {
	var result json.RawMessage
	var state *rsiplugin.State
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, current, err := stateFor(ctx, tx, run, instance)
		if err != nil {
			return err
		}
		var old string
		var value sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM rsi_call WHERE run_id=? AND instance_id=? AND operation_id=?`, run, instance, key).Scan(&old, &value)
		if err == nil {
			if old != hash {
				return rsi.ErrConflict
			}
			if !value.Valid {
				return rsi.ErrUnknown
			}
			result = json.RawMessage(value.String)
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO rsi_call VALUES(?,?,?,?,?,NULL)`, run, instance, key, hash, current.Version); err != nil {
			return err
		}
		state = &current
		return audit(ctx, tx, "call.accepted", key, struct{ Run, Instance string }{run, instance})
	})
	return result, state, err
}
func (s *Store) FinishCall(ctx context.Context, run, instance, key string, old rsiplugin.State, change *json.RawMessage, value json.RawMessage) error {
	if !json.Valid(value) {
		return rsi.ErrInvalid
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		namespace, current, err := stateFor(ctx, tx, run, instance)
		if err != nil {
			return err
		}
		if current.Version != old.Version {
			return rsi.ErrConflict
		}
		if change != nil {
			res, e := tx.ExecContext(ctx, `UPDATE rsi_state SET value_json=?,version=version+1 WHERE namespace=? AND version=?`, string(*change), namespace, old.Version)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return rsi.ErrConflict
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE rsi_call SET result_json=? WHERE run_id=? AND instance_id=? AND operation_id=? AND state_version=? AND result_json IS NULL`, string(value), run, instance, key, old.Version)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return rsi.ErrConflict
		}
		return audit(ctx, tx, "call.completed", key, struct{ Run, Instance string }{run, instance})
	})
}

var _ rsi.Store = (*Store)(nil)
