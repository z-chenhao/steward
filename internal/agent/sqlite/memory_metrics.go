package sqlite

import (
	"context"
	"database/sql"
	"github.com/z-chenhao/assistant/internal/memory"
)

func (r *Runs) memoryMetrics(ctx context.Context, event string) (memory.MaintenanceMetrics, error) {
	m := memory.MaintenanceMetrics{Costs: []memory.MaintenanceCost{}}
	e := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		e := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM memory_generation g WHERE g.event_id=e.event_id),
 (SELECT count(*) FROM memory_dream_change c WHERE c.event_id=e.event_id AND operation='write'),
 (SELECT count(*) FROM memory_dream_change c WHERE c.event_id=e.event_id AND operation='retire'),
 (SELECT count(*) FROM memory_dream_change c WHERE c.event_id=e.event_id AND operation='unchanged'),
 (SELECT count(*) FROM memory_access a WHERE a.agent_id=e.agent_id AND a.created_at>=e.accepted_at AND EXISTS(SELECT 1 FROM memory_dream_change c WHERE c.event_id=e.event_id AND c.operation='write' AND c.memory_ref=a.source_ref)),
 (SELECT count(*) FROM memory_injection_access a JOIN run r USING(run_id) WHERE r.agent_id=e.agent_id AND a.created_at>=e.accepted_at AND EXISTS(SELECT 1 FROM memory_dream_change c WHERE c.event_id=e.event_id AND c.operation='write' AND c.memory_ref=a.memory_ref)),
 (SELECT count(*) FROM memory_generation g LEFT JOIN memory_cost_reservation c USING(generation_id) WHERE g.event_id=e.event_id AND (c.status IS NULL OR c.status<>'priced'))
 FROM memory_mutation e WHERE e.event_id=? AND e.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, event).Scan(&m.Generations, &m.DocumentsWritten, &m.DocumentsRetired, &m.UnchangedWrites, &m.LaterToolReads, &m.LaterInjections, &m.UnpricedGenerations)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT coalesce(sum(u.input_tokens),0),coalesce(sum(u.output_tokens),0),coalesce(sum(u.cache_read_tokens),0),count(u.input_tokens),coalesce(sum(u.latency_ms),0) FROM memory_generation g JOIN provider_usage u USING(generation_id) WHERE g.event_id=?`, event).Scan(&m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.KnownUsageGenerations, &m.ModelMilliseconds); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT c.currency,sum(c.actual_micros),count(*) FROM memory_cost_reservation c JOIN memory_generation g USING(generation_id) WHERE g.event_id=? AND g.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND c.status='priced' GROUP BY c.currency ORDER BY c.currency`, event)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var cost memory.MaintenanceCost
			if e = rows.Scan(&cost.Currency, &cost.ReferenceMicros, &cost.PricedGenerations); e != nil {
				return e
			}
			m.Costs = append(m.Costs, cost)
		}
		return rows.Err()
	})
	return m, e
}
