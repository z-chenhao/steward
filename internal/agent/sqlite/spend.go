package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
)

func (r *Runs) ConfigureSpend(p agent.SpendPolicy) { r.spend = p }

func spendAccount(s llm.Snapshot) string {
	id := s.AuthConnectionID
	if id == "" {
		id = s.EndpointHash
	}
	return s.ProviderKind + ":" + id
}

// This immutable companion belongs to the ModelSnapshot, not a live pricing
// lookup. Children inherit it by sharing their Parent's snapshot identity.
func (r *Runs) freezeSpend(ctx context.Context, tx *sql.Tx, s llm.Snapshot) error {
	currency, boundary, price := "", int64(0), "null"
	for _, a := range r.spend.Accounts {
		if a.Account != spendAccount(s) {
			continue
		}
		currency, boundary = a.Currency, a.Boundary
		for _, p := range a.Prices {
			if p.Model == s.ModelID {
				price, _ = encoded(p)
				break
			}
		}
	}
	if currency != "" {
		var conflicts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_snapshot WHERE account_key=? AND currency!='' AND currency!=?`, spendAccount(s), currency).Scan(&conflicts); err != nil {
			return err
		}
		if conflicts != 0 {
			return agent.ErrConflict
		} // no implicit FX conversion
		if price != "null" {
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_snapshot p JOIN model_snapshot m USING(snapshot_id) WHERE p.account_key=? AND m.model_id=? AND json_extract(p.price_json,'$.version')=json_extract(?,'$.version') AND p.price_json!=?`, spendAccount(s), s.ModelID, price, price).Scan(&conflicts); err != nil {
				return err
			}
			if conflicts != 0 {
				return agent.ErrConflict
			}
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO spend_snapshot VALUES(?,?,?,?,?)`, s.SnapshotID, spendAccount(s), currency, boundary, price)
	return err
}

// Pi's input/cache-read/cache-write categories are disjoint. Round the full
// dot product upward once, using integers so even hostile counts cannot wrap.
func monetaryCost(p agent.ModelPrice, u llm.TokenUsage) (int64, error) {
	total := new(big.Int)
	for _, pair := range [][2]int64{{p.Input, u.InputTokens}, {p.Output, u.OutputTokens}, {p.CacheRead, u.CacheReadTokens}, {p.CacheWrite, u.CacheWriteTokens}} {
		if pair[0] < 0 || pair[1] < 0 {
			return 0, agent.ErrStorage
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(pair[0]), big.NewInt(pair[1])))
	}
	total.Add(total, big.NewInt(999999)).Quo(total, big.NewInt(1000000))
	if !total.IsInt64() {
		return 0, agent.ErrStorage
	}
	return total.Int64(), nil
}

func projectedCost(price agent.ModelPrice, snapshot llm.Snapshot) (int64, error) {
	maxInput := max(price.Input, price.CacheRead, price.CacheWrite)
	return monetaryCost(agent.ModelPrice{Input: maxInput, Output: price.Output}, llm.TokenUsage{InputTokens: snapshot.ContextWindow, OutputTokens: snapshot.MaxOutputTokens})
}

// gateSpend runs in generation admission's immediate transaction. A held
// generation has no provider dispatch and consumes no reservation. Existing
// reservations are reattached, never double counted after process death.
func (r *Runs) gateSpend(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest) (bool, bool, error) {
	var account, currency, raw string
	var boundary int64
	err := tx.QueryRowContext(ctx, `SELECT account_key,currency,boundary_micros,price_json FROM spend_snapshot WHERE snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&account, &currency, &boundary, &raw)
	if err == sql.ErrNoRows {
		return false, false, agent.ErrStorage
	} // migration backfills legacy snapshots; an absent companion is corruption
	if err != nil {
		return false, false, err
	}
	var price *agent.ModelPrice
	if json.Unmarshal([]byte(raw), &price) != nil {
		return false, false, agent.ErrStorage
	}
	if price == nil {
		return false, false, nil
	} // unpriced: accounting only
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_reservation WHERE generation_id=?`, req.GenerationID).Scan(&existing); err != nil {
		return false, false, err
	}
	if existing > 0 {
		return false, false, nil
	}
	projected, err := projectedCost(*price, req.Snapshot)
	if err != nil {
		return false, false, err
	}
	if boundary > 0 {
		var owner string
		if err = tx.QueryRowContext(ctx, `SELECT coalesce(root_run_id,run_id) FROM run WHERE run_id=?`, req.RunID).Scan(&owner); err != nil {
			return false, false, err
		}
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM spend_approval WHERE root_run_id=?`, owner).Scan(&status)
		if err != nil && err != sql.ErrNoRows {
			return false, false, err
		}
		if status == "denied" || status == "invalidated" {
			return false, true, nil
		}
		if status == "pending" {
			return true, false, nil
		}
		if status != "approved" {
			var total int64
			if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN status='priced' THEN actual_micros ELSE reserved_micros END),0) FROM (SELECT account_key,currency,status,actual_micros,reserved_micros FROM spend_reservation UNION ALL SELECT account_key,currency,status,actual_micros,reserved_micros FROM memory_cost_reservation) WHERE account_key=? AND currency=?`, account, currency).Scan(&total); err != nil {
				return false, false, err
			}
			if total >= boundary || projected >= boundary-total {
				if err := r.requestSpendApproval(ctx, tx, req, *price, boundary, total, projected); err != nil {
					return false, false, err
				}
				return true, false, nil
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO spend_reservation VALUES(?,?,?,?,NULL,'reserved')`, req.GenerationID, account, currency, projected)
	return false, false, err
}

// requestSpendApproval is shared by projected admission and actual settlement.
// Both execute inside the existing fenced model transaction.
func (r *Runs) requestSpendApproval(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest, price agent.ModelPrice, boundary, total, projected int64) error {
	var owner, scope, account, currency, title string
	var epoch sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT coalesce(r.root_run_id,r.run_id),r.agent_id,r.task_epoch,s.account_key,s.currency,coalesce(t.title,'Current conversation') FROM run r JOIN spend_snapshot s ON s.snapshot_id=r.model_snapshot_id LEFT JOIN task t ON t.task_id=r.task_id WHERE r.run_id=?`, req.RunID).Scan(&owner, &scope, &epoch, &account, &currency, &title)
	if err != nil {
		return err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_approval WHERE root_run_id=?`, owner).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	var unpriced, missing int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_unpriced_usage WHERE account_key=?`, account).Scan(&unpriced); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_reservation WHERE account_key=? AND status='usage_missing'`, account).Scan(&missing); err != nil {
		return err
	}
	argsJSON, _ := json.Marshal(struct {
		Root          string `json:"batch_run_id"`
		Title         string `json:"batch_title"`
		Epoch         int64  `json:"task_epoch"`
		Snapshot      string `json:"model_snapshot_id"`
		Account       string `json:"account"`
		Currency      string `json:"currency"`
		Boundary      int64  `json:"boundary_micros"`
		Committed     int64  `json:"account_committed_micros"`
		Projected     int64  `json:"next_call_max_micros"`
		Pricing       string `json:"pricing_version"`
		Unpriced      int64  `json:"unpriced_calls"`
		Missing       int64  `json:"missing_usage_calls"`
		Authorization string `json:"authorization"`
	}{owner, title, epoch.Int64, req.Snapshot.SnapshotID, account, currency, boundary, total, projected, price.Version, unpriced, missing, "continue this Run batch beyond the account boundary without another monetary ceiling; model spend only; tool effects excluded"})
	args := string(argsJSON)
	id := newID()
	hash := digest(args)
	if _, err = tx.ExecContext(ctx, `INSERT INTO spend_approval VALUES(?,?,?,?,?,?,'pending',?,NULL)`, id, owner, scope, epoch, args, hash, time.Now().UnixMicro()); err != nil {
		return err
	}
	quotedTitle, _ := json.Marshal(title)
	// The frozen action retains internal identity for fencing. The ordinary
	// notice shows the decision, amounts and unknowns, without Run/trace IDs.
	money := func(v int64) string { return fmt.Sprintf("%d.%06d", v/1000000, v%1000000) }
	text := "Account spend approval required: " + string(quotedTitle) + "\nCurrency: " + currency + "; pricing: " + price.Version + "; boundary: " + money(boundary) + ".\nAt request time, known spend plus reservations: " + money(total) + "; per-call maximum: " + money(projected) + fmt.Sprintf(".\nUnpriced calls: %d; calls with unknown actual cost: %d.", unpriced, missing) + "\nApprove remaining model spending for this batch beyond the account boundary, without another monetary ceiling. Tool effects require separate approval.\nUse /approve " + id + " " + hash + " or /deny " + id + " " + hash
	if err = r.notification(ctx, tx, owner, "spend:"+id, "approval", "security", text); err != nil {
		return err
	}
	if err = runEvent(ctx, tx, owner, "spend_approval.requested", 1); err != nil {
		return err
	}
	return nil
}

// Usage, monetary settlement, and the accepted generation commit together.
// Missing usage keeps the known reservation; it never becomes zero cost.
func (s *Store) settleSpend(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest, result llm.GenerationResult) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT price_json FROM spend_snapshot WHERE snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&raw)
	if err != nil {
		return err
	}
	if raw == "null" {
		return nil
	}
	var reserved int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_reservation WHERE generation_id=? AND status='reserved'`, req.GenerationID).Scan(&reserved); err != nil {
		return err
	}
	if reserved != 1 {
		return agent.ErrStorage
	}
	var price agent.ModelPrice
	if json.Unmarshal([]byte(raw), &price) != nil {
		return agent.ErrStorage
	}
	if result.Usage == nil {
		_, err = tx.ExecContext(ctx, `UPDATE spend_reservation SET status='usage_missing' WHERE generation_id=?`, req.GenerationID)
	} else {
		var amount int64
		amount, err = monetaryCost(price, *result.Usage)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE spend_reservation SET status='priced',actual_micros=? WHERE generation_id=?`, amount, req.GenerationID)
		}
	}
	if err != nil {
		return err
	}
	var boundary, total int64
	if err = tx.QueryRowContext(ctx, `SELECT s.boundary_micros,(SELECT coalesce(sum(CASE WHEN v.status='priced' THEN v.actual_micros ELSE v.reserved_micros END),0) FROM (SELECT account_key,currency,status,actual_micros,reserved_micros FROM spend_reservation UNION ALL SELECT account_key,currency,status,actual_micros,reserved_micros FROM memory_cost_reservation) v WHERE v.account_key=s.account_key AND v.currency=s.currency) FROM spend_snapshot s WHERE s.snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&boundary, &total); err != nil {
		return err
	}
	if boundary > 0 && total >= boundary {
		projected, err := projectedCost(price, req.Snapshot)
		if err != nil {
			return err
		}
		return (&Runs{host: s}).requestSpendApproval(ctx, tx, req, price, boundary, total, projected)
	}
	return nil
}

func (r *Runs) decideSpend(ctx context.Context, tx *sql.Tx, principal, id, hash, decision string) (bool, error) {
	var status, owner string
	err := tx.QueryRowContext(ctx, `SELECT status,root_run_id FROM spend_approval WHERE approval_id=? AND scope=? AND frozen_hash=?`, id, principal, hash).Scan(&status, &owner)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if status == decision {
		return true, nil
	}
	if status != "pending" {
		return true, agent.ErrConflict
	}
	var valid int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM spend_approval a JOIN run r ON r.run_id=a.root_run_id WHERE a.approval_id=? AND r.agent_id=? AND r.desired_state='running' AND r.status IN ('queued','running','waiting') AND (a.task_epoch IS NULL OR EXISTS(SELECT 1 FROM task t WHERE t.task_id=r.task_id AND t.task_epoch=a.task_epoch AND t.status='active'))`, id, principal).Scan(&valid); err != nil {
		return true, err
	}
	if valid != 1 {
		return true, agent.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE spend_approval SET status=?,decided_at=? WHERE approval_id=? AND status='pending'`, decision, time.Now().UnixMicro(), id); err != nil {
		return true, err
	}
	// Resource waiting is used for monetary holds, distinct from invocation
	// effect approvals. The scheduler's predicate resolves the durable decision.
	return true, runEvent(ctx, tx, owner, "spend_approval.decided", 1)
}

// Spend is a local human control-plane read. No model-visible tool is added.
func (r *Runs) Spend(ctx context.Context) ([]agent.SpendView, error) {
	rows, err := r.host.db.QueryContext(ctx, `WITH snapshots AS (
 SELECT * FROM spend_snapshot UNION ALL
 SELECT DISTINCT u.snapshot_id,u.account_key,'',0,'null' FROM spend_unpriced_usage u WHERE NOT EXISTS(SELECT 1 FROM spend_snapshot f WHERE f.snapshot_id=u.snapshot_id)
 ) SELECT s.snapshot_id,coalesce(json_extract(s.price_json,'$.version'),''),s.account_key,s.currency,CASE WHEN s.price_json='null' AND s.boundary_micros=0 THEN 'unpriced_unconfigured' WHEN s.price_json='null' THEN 'unpriced' WHEN s.boundary_micros=0 THEN 'unconfigured' ELSE 'configured' END,s.boundary_micros,
 (SELECT coalesce(sum(CASE WHEN v.status='priced' THEN v.actual_micros ELSE v.reserved_micros END),0) FROM (SELECT account_key,currency,status,actual_micros,reserved_micros FROM spend_reservation UNION ALL SELECT account_key,currency,status,actual_micros,reserved_micros FROM memory_cost_reservation) v WHERE v.account_key=s.account_key AND v.currency=s.currency),
 (SELECT count(*) FROM spend_reservation v WHERE v.account_key=s.account_key AND v.currency=s.currency AND v.status='usage_missing'),
 (SELECT count(*) FROM spend_unpriced_usage u WHERE u.account_key=s.account_key)
 FROM snapshots s ORDER BY s.account_key,s.snapshot_id`)
	if err != nil {
		return nil, agent.ErrStorage
	}
	defer rows.Close()
	out := []agent.SpendView{}
	for rows.Next() {
		var v agent.SpendView
		if rows.Scan(&v.Snapshot, &v.PricingVersion, &v.Account, &v.Currency, &v.State, &v.Boundary, &v.Committed, &v.MissingUsage, &v.UnpricedCalls) != nil {
			return nil, agent.ErrStorage
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, agent.ErrStorage
	}
	if len(out) == 0 {
		out = append(out, agent.SpendView{State: "unpriced_unconfigured"})
	}
	return out, nil
}
