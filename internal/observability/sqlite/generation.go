package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/observability"
)

func generationDetails(ctx context.Context, tx *sql.Tx, id string) (*observability.GenerationDetails, error) {
	d := &observability.GenerationDetails{}
	var input, output, read, write, latency sql.NullInt64
	var currency, source sql.NullString
	var cost sql.NullInt64
	var estimated bool
	err := tx.QueryRowContext(ctx, `SELECT
 coalesce(json_extract(g.request_json,'$.model_snapshot.model_id'),''),
 coalesce(json_extract(g.request_json,'$.model_snapshot.provider_kind'),''),
 coalesce(json_extract(g.request_json,'$.model_snapshot.wire_api'),''),
 coalesce(json_extract(g.request_json,'$.model_snapshot.reasoning_effort'),''),
 coalesce(json_extract(g.request_json,'$.model_snapshot.generation_defaults'),'{}'),
 coalesce(json_extract(g.request_json,'$.model_snapshot.max_output_tokens'),0),
 u.input_tokens,u.output_tokens,u.cache_read_tokens,u.cache_write_tokens,u.latency_ms,
 coalesce(s.actual_micros,e.estimated_micros),
 CASE WHEN s.actual_micros IS NOT NULL THEN s.currency ELSE e.currency END,
 s.actual_micros IS NULL AND e.generation_id IS NOT NULL,
 CASE WHEN s.actual_micros IS NULL THEN e.source||' · '||json_extract(e.price_json,'$.version') ELSE '' END
 FROM model_generation g LEFT JOIN provider_usage u USING(generation_id)
 LEFT JOIN spend_reservation s USING(generation_id)
 LEFT JOIN generation_cost_estimate e USING(generation_id)
 WHERE g.generation_id=?`, id).Scan(&d.Model, &d.Provider, &d.API, &d.ReasoningEffort, &d.Parameters, &d.MaxOutputTokens,
		&input, &output, &read, &write, &latency, &cost, &currency, &estimated, &source)
	if err != nil {
		return nil, err
	}
	d.Metrics.Generations = 1
	if input.Valid && output.Valid && read.Valid && write.Valid {
		d.Metrics.KnownUsage = 1
		d.Metrics.Input, d.Metrics.Output, d.Metrics.CacheRead, d.Metrics.CacheWrite = input.Int64, output.Int64, read.Int64, write.Int64
	}
	if latency.Valid {
		d.Metrics.ProviderMS = &latency.Int64
	}
	if cost.Valid && currency.Valid {
		d.Metrics.Costs = []observability.Cost{{Currency: currency.String, Micros: cost.Int64, Estimated: estimated, Source: source.String}}
	} else {
		d.Metrics.Unpriced = 1
	}
	return d, nil
}
