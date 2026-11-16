package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type referenceRatesPlan struct{ revision, outcome, raw string }

// No prompt, endpoint URL, credential or actual usage crosses this read seam.
// A missing/invalid producer freezes an unavailable estimate, never native rates.
func (r *Runs) prepareReferenceRates(ctx context.Context, c agent.RunClaim, req llm.GenerationRequest, step int64) (*referenceRatesPlan, error) {
	if req.RunID != c.RunID() {
		return nil, agent.ErrClaimLost
	}
	var exists bool
	var err error
	if req.ToolGeneration != nil {
		err = r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.invocation_id')=? AND json_extract(request_json,'$.tool_generation.call_key')=?)`, req.RunID, req.ToolGeneration.InvocationID, req.ToolGeneration.CallKey).Scan(&exists)
	} else {
		err = r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_generation WHERE run_id=? AND step_index=?)`, req.RunID, step).Scan(&exists)
	}
	if err != nil || exists {
		return nil, err
	} // Frozen/predating generations are not reinterpreted.
	managed, err := r.managedRunService(ctx, req.RunID, contract.Service)
	if err != nil || !managed {
		return nil, err
	}
	p := &referenceRatesPlan{outcome: "unavailable", raw: "null"}
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		var revision sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT graph_revision FROM rsi_run WHERE run_id=?`, req.RunID).Scan(&revision); err != nil {
			return err
		}
		p.revision = revision.String
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Manual monetary policy is authoritative; do not ask an observational producer.
	var configured bool
	if err = r.host.db.QueryRowContext(ctx, `SELECT price_json!='null' FROM spend_snapshot WHERE snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&configured); err != nil {
		return nil, err
	}
	if configured {
		p.outcome = "unknown"
		return p, nil
	}
	if r.plugins == nil || p.revision == "" {
		return p, nil
	}
	input, _ := json.Marshal(contract.Request{ProviderKind: req.Snapshot.ProviderKind, EndpointHash: req.Snapshot.EndpointHash, WireAPI: req.Snapshot.WireAPI, ModelID: req.Snapshot.ModelID})
	h := r.pluginHost(c)
	raw, err := h.ReadService(ctx, p.revision, contract.Service, input, nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return p, nil
	}
	var out contract.Response
	if rsiplugin.DecodeResult(raw, &out) != nil || !validReferenceTable(out.Table) {
		return p, nil
	}
	p.outcome = "unknown"
	if out.Table != nil {
		p.outcome = "known"
		data, _ := json.Marshal(out.Table)
		p.raw = string(data)
	}
	return p, nil
}
func validReferenceTable(t *contract.Table) bool {
	if t == nil {
		return true
	}
	if t.Currency != "USD" || t.Source == "" || len(t.Source) > 512 || !utf8.ValidString(t.Source) || t.Version == "" || len(t.Version) > 256 || !utf8.ValidString(t.Version) || len(t.Tiers) > 64 {
		return false
	}
	valid := func(r contract.Rates) bool {
		return r.Input >= 0 && r.Output >= 0 && r.CacheRead >= 0 && r.CacheWrite >= 0
	}
	if !valid(t.Base) {
		return false
	}
	previous := int64(-1)
	for _, tier := range t.Tiers {
		if tier.Above <= previous || !valid(tier.Rates) {
			return false
		}
		previous = tier.Above
	}
	return true
}
func acceptReferenceRates(ctx context.Context, tx *sql.Tx, id string, p *referenceRatesPlan) error {
	if p == nil {
		return nil
	}
	var revision any
	if p.revision != "" {
		revision = p.revision
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO generation_reference_rates(generation_id,graph_revision,outcome,rates_json) VALUES(?,?,?,?)`, id, revision, p.outcome, p.raw)
	return err
}
func frozenReferencePrice(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest, usage llm.TokenUsage) (agent.ReferencePrice, bool, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT rates_json FROM generation_reference_rates WHERE generation_id=?`, req.GenerationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		var managed bool
		if err = tx.QueryRowContext(ctx, `SELECT reference_rates_managed FROM model_generation WHERE generation_id=?`, req.GenerationID).Scan(&managed); err != nil {
			return agent.ReferencePrice{}, false, true, err
		}
		if managed {
			return agent.ReferencePrice{}, false, true, agent.ErrStorage
		}
		return agent.ReferencePrice{}, false, false, nil
	}
	if err != nil {
		return agent.ReferencePrice{}, false, true, err
	}
	if raw == "null" || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 {
		return agent.ReferencePrice{}, false, true, nil
	}
	var table contract.Table
	if rsiplugin.DecodeResult([]byte(raw), &table) != nil || !validReferenceTable(&table) {
		return agent.ReferencePrice{}, false, true, agent.ErrStorage
	}
	count := new(big.Int).Add(big.NewInt(usage.InputTokens), big.NewInt(usage.CacheReadTokens))
	count.Add(count, big.NewInt(usage.CacheWriteTokens))
	rates := table.Base
	for _, tier := range table.Tiers {
		if count.Cmp(big.NewInt(tier.Above)) > 0 {
			rates = tier.Rates
		}
	}
	return agent.ReferencePrice{Currency: table.Currency, Source: table.Source, Price: agent.ModelPrice{Model: req.Snapshot.ModelID, Version: table.Version, Input: rates.Input, Output: rates.Output, CacheRead: rates.CacheRead, CacheWrite: rates.CacheWrite}}, true, true, nil
}
