package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (s Inspector) traceActivityPlan(ctx context.Context, tx *sql.Tx, q observability.TraceQuery, first, last sql.NullInt64) (contract.ActivityPlan, func(context.Context) error, error) {
	facts := contract.ActivityFacts{}
	if first.Valid {
		value := first.Int64
		facts.First = &value
	}
	if last.Valid {
		value := last.Int64
		facts.Last = &value
	}
	if !q.Since.IsZero() {
		value := q.Since.UnixMicro()
		facts.Since = &value
	}
	if !q.Until.IsZero() {
		value := q.Until.UnixMicro()
		facts.Until = &value
	}
	host, revision, managed, err := s.traceReadSelection(ctx, tx, q.ReaderRevision)
	if err != nil {
		return contract.ActivityPlan{}, nil, err
	}
	if !managed {
		return legacyTraceActivityPlanV1(facts), nil, nil
	}
	check := func(c context.Context) error { return host.Store.CheckGraph(c, revision) }
	_, err = host.ServiceInstance(ctx, revision, contract.Activity)
	var plan contract.ActivityPlan
	if errors.Is(err, rsi.ErrNotFound) {
		binding, e := host.ServiceInstance(ctx, revision, contract.Contract)
		if e != nil {
			return plan, nil, e
		}
		pkg, e := host.Catalog.Load(binding.PackageDigest)
		if e != nil {
			return plan, nil, e
		}
		if pkg.Manifest.PluginID != contract.PluginID || pkg.Manifest.Version != "1.0.1" {
			return plan, nil, rsi.ErrRevoked
		}
		if e = check(ctx); e != nil {
			return plan, nil, e
		}
		plan = legacyTraceActivityPlanV1(facts)
	} else {
		if err != nil {
			return plan, nil, err
		}
		data, e := json.Marshal(facts)
		if e != nil {
			return plan, nil, e
		}
		raw, e := host.ReadService(ctx, revision, contract.Activity, json.RawMessage(`{"input_id":"observations"}`), []rsi.InputData{{ID: "observations", Data: data}})
		if e != nil {
			return plan, nil, e
		}
		if e = rsiplugin.DecodeResult(raw, &plan); e != nil {
			return plan, nil, rsi.ErrInvalid
		}
	}
	if err = acceptTraceActivityPlan(facts, plan); err != nil {
		return plan, nil, err
	}
	return plan, check, nil
}

// Only uniform numeric counting recipes cross this seam. SQL predicates, scoped
// records, actual counts and the live privacy/Guardian fence remain Host-owned.
func acceptTraceActivityPlan(f contract.ActivityFacts, p contract.ActivityPlan) error {
	present := f.First != nil || f.Since != nil && f.Until != nil
	if p.Present != present {
		return rsi.ErrInvalid
	}
	if !present {
		if p != (contract.ActivityPlan{}) {
			return rsi.ErrInvalid
		}
		return nil
	}
	if p.Step <= 0 || p.End <= p.Start || p.End-p.Start <= 0 {
		return rsi.ErrInvalid
	}
	if f.Since != nil && p.Start != *f.Since || f.Until != nil && p.End != *f.Until || f.First != nil && (p.Start > *f.First || p.End <= *f.Last) {
		return rsi.ErrInvalid
	}
	if p.Base != p.Start {
		gap := p.Base - p.Start
		if p.Base < p.Start {
			gap = p.Start - p.Base
		}
		if gap <= 0 || gap >= p.Step {
			return rsi.ErrInvalid
		}
	}
	count := 0
	for at := p.Base; at < p.End; {
		if count >= 48 || at > math.MaxInt64-p.Step {
			return rsi.ErrInvalid
		}
		count++
		at += p.Step
	}
	return nil
}
