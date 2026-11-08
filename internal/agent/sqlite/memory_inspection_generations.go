package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

func (r *Runs) memoryInspectionGenerations(ctx context.Context, event string, access evidenceAccess) ([]memory.MaintenanceGeneration, error) {
	var out []memory.MaintenanceGeneration
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT g.generation_id,g.purpose,g.pass_index,g.status,g.created_at,coalesce(s.model_id,json_extract(u.snapshot_json,'$.model_id'),''),u.input_tokens,u.output_tokens,u.latency_ms,coalesce(g.result_sealed,''),g.privacy_epoch,coalesce(u.error_code,'') FROM memory_generation g JOIN memory_mutation e USING(event_id) LEFT JOIN provider_usage u USING(generation_id) LEFT JOIN model_snapshot s ON s.snapshot_id=g.model_snapshot_id WHERE g.event_id=? AND g.agent_id=? AND e.agent_id=g.agent_id ORDER BY g.created_at DESC,g.generation_id DESC LIMIT 50`, event, access.scope.AgentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g memory.MaintenanceGeneration
			var at, epoch int64
			var sealed string
			if err := rows.Scan(&g.ID, &g.Purpose, &g.Pass, &g.Status, &at, &g.Model, &g.InputTokens, &g.OutputTokens, &g.LatencyMS, &sealed, &epoch, &g.Failure); err != nil {
				return err
			}
			g.CreatedAt = time.UnixMicro(at).UTC()
			if g.Status == "failed" && sealed != "" && at > access.floor && epoch == access.epoch {
				plain, err := r.vault.Open(ctx, sealed, g.ID+":result")
				var failure memoryFailure
				if err != nil || json.Unmarshal([]byte(plain), &failure) != nil {
					return memory.ErrInspectionUnavailable
				}
				g.Failure = failure.Reason
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}
