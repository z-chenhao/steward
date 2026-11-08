package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

const traceFields = executionColumns + `,r.conversation_id,coalesce(substr(t.title,1,160),'Main conversation'),m.model_id,
 coalesce((SELECT substr(json_extract(i.message_json,'$.content[0].text'),1,160) FROM main_input i WHERE i.run_id=r.run_id ORDER BY i.sequence LIMIT 1),substr(t.title,1,160),''),
 coalesce((SELECT substr(json_extract(g.result_json,'$.text'),1,160) FROM model_generation g WHERE g.run_id=r.run_id AND g.status='completed' AND NOT EXISTS(SELECT 1 FROM context_compaction c WHERE c.run_id=g.run_id AND c.step_index=g.step_index AND json_extract(c.provenance_json,'$.stage')='summary_request') ORDER BY g.step_index DESC LIMIT 1),'')`
const traceFrom = ` FROM run r LEFT JOIN task t ON t.task_id=r.task_id JOIN model_snapshot m ON m.snapshot_id=r.model_snapshot_id WHERE r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND (r.kind='main' OR r.worker_position='root')`

func scanTrace(row scanner) (observability.Trace, error) {
	var t observability.Trace
	var created int64
	var started, ended sql.NullInt64
	err := row.Scan(&t.ID, &t.Role, &t.Status, &t.WaitReason, &t.Parent, &t.Root, &t.Depth, &t.Epoch, &t.TaskRef, &created, &started, &ended, &t.Session, &t.Title, &t.Model, &t.Input, &t.Output)
	t.CreatedAt = stamp(created)
	if started.Valid {
		t.StartedAt = stamp(started.Int64)
	}
	if ended.Valid {
		t.EndedAt = stamp(ended.Int64)
	}
	return t, err
}

func traceRoot(ctx context.Context, tx *sql.Tx, id string) (observability.Trace, error) {
	var root string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN kind='main' THEN run_id ELSE root_run_id END FROM run WHERE run_id=? AND agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, id).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return observability.Trace{}, observability.ErrNotFound
	}
	if err != nil {
		return observability.Trace{}, err
	}
	t, err := scanTrace(tx.QueryRowContext(ctx, `SELECT `+traceFields+traceFrom+` AND r.run_id=?`, root))
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	return t, err
}

func (s Inspector) Traces(ctx context.Context, q observability.TraceQuery) (observability.Traces, error) {
	out := observability.Traces{Items: []observability.Trace{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	c, err := readCursor(q.Cursor, "traces:"+q.Session)
	if err != nil {
		return out, err
	}
	query := `SELECT ` + traceFields + traceFrom
	args := []any{}
	if q.Search != "" {
		// Literal substring search over trace identity; never scan observation bodies.
		query += ` AND (instr(lower(r.run_id),lower(?))>0 OR instr(lower(coalesce(t.title,'Main conversation')),lower(?))>0 OR instr(lower(m.model_id),lower(?))>0)`
		args = append(args, q.Search, q.Search, q.Search)
	}
	if q.Session != "" {
		query += ` AND r.conversation_id=?`
		args = append(args, q.Session)
	}
	if q.Role != "" {
		query += ` AND CASE WHEN r.kind='main' THEN 'main' ELSE r.worker_position END=?`
		args = append(args, q.Role)
	}
	if q.Status != "" {
		query += ` AND r.status=?`
		args = append(args, q.Status)
	}
	if !q.Since.IsZero() {
		query += ` AND r.created_at>=?`
		args = append(args, q.Since.UnixMicro())
	}
	if !q.Until.IsZero() {
		query += ` AND r.created_at<?`
		args = append(args, q.Until.UnixMicro())
	}
	filtered := strings.TrimPrefix(query, `SELECT `+traceFields)
	filterArgs := append([]any(nil), args...)
	if c.ID != "" {
		query += ` AND (r.created_at,r.run_id)<(?,?)`
		args = append(args, c.At, c.ID)
	}
	query += ` ORDER BY r.created_at DESC,r.run_id DESC LIMIT ?`
	limit := pageLimit(q.Limit)
	args = append(args, limit+1)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var first, last sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),min(r.created_at),max(r.created_at)`+filtered, filterArgs...).Scan(&out.Total, &first, &last); err != nil {
		return out, err
	}
	if first.Valid || (!q.Since.IsZero() && !q.Until.IsZero()) {
		start, end := first.Int64, last.Int64+1
		if !q.Since.IsZero() {
			start = q.Since.UnixMicro()
		}
		if !q.Until.IsZero() {
			end = q.Until.UnixMicro()
		}
		// A useful minimum window avoids dozens of indistinguishable microsecond
		// buckets for a single trace. Explicit filter boundaries remain exact.
		if q.Since.IsZero() && q.Until.IsZero() && end-start < 60_000_000 {
			end = start + 60_000_000
		}
		step := activityInterval(end - start)
		base := start / step * step
		for at := base; at < end; at += step {
			out.Activity = append(out.Activity, observability.TraceBucket{At: stamp(max(start, at)), Until: stamp(min(end, at+step))})
		}
		bucketArgs := append([]any{base, step}, filterArgs...)
		buckets, e := tx.QueryContext(ctx, `SELECT (r.created_at-?)/?,count(*)`+filtered+` GROUP BY 1`, bucketArgs...)
		if e != nil {
			return out, e
		}
		for buckets.Next() {
			var bucket int
			var count int64
			if e = buckets.Scan(&bucket, &count); e != nil {
				buckets.Close()
				return out, e
			}
			if bucket >= 0 && bucket < len(out.Activity) {
				out.Activity[bucket].Count = count
			}
		}
		err = buckets.Err()
		buckets.Close()
		if err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		t, e := scanTrace(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if len(out.Items) == limit {
			last := out.Items[limit-1]
			at, _ := parseStamp(last.CreatedAt)
			out.Next = nextCursor("traces:"+q.Session, at, last.ID)
			break
		}
		out.Items = append(out.Items, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range out.Items {
		out.Items[i].Metrics, err = traceMetrics(ctx, tx, out.Items[i].ID)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Only unique accepted generations contribute usage/cost. Reuse and HTTP
// retries never multiply these ledgers; absent usage/pricing remain unknown.
func traceMetrics(ctx context.Context, tx *sql.Tx, id string) (observability.Metrics, error) {
	m := observability.Metrics{Costs: []observability.Cost{}}
	var ms sql.NullInt64
	const scope = ` FROM model_generation g JOIN run r USING(run_id) LEFT JOIN provider_usage u USING(generation_id) LEFT JOIN spend_reservation s USING(generation_id) LEFT JOIN generation_cost_estimate e USING(generation_id) WHERE (r.run_id=? OR r.root_run_id=?)`
	err := tx.QueryRowContext(ctx, `SELECT count(*),count(u.input_tokens),coalesce(sum(u.input_tokens),0),coalesce(sum(u.output_tokens),0),coalesce(sum(u.cache_read_tokens),0),coalesce(sum(u.cache_write_tokens),0),sum(u.latency_ms),count(*)-count(coalesce(s.actual_micros,e.estimated_micros))`+scope, id, id).Scan(&m.Generations, &m.KnownUsage, &m.Input, &m.Output, &m.CacheRead, &m.CacheWrite, &ms, &m.Unpriced)
	if err != nil {
		return m, err
	}
	if ms.Valid {
		m.ProviderMS = &ms.Int64
	}
	// The first generation defines trace TTFT. Text, reasoning and tool-call
	// deltas are model output; HTTP headers and protocol-only frames are not.
	var firstText sql.NullInt64
	err = tx.QueryRowContext(ctx, `WITH first_generation AS (
 SELECT g.generation_id FROM model_generation g JOIN run r USING(run_id)
 WHERE (r.run_id=? OR r.root_run_id=?) ORDER BY g.created_at,g.generation_id LIMIT 1
 ), timing AS (
 SELECT (SELECT min(occurred_at) FROM dispatch_observation WHERE generation_id=(SELECT generation_id FROM first_generation) AND phase='started') AS started,
 (SELECT min(t.occurred_at) FROM dispatch_observation t JOIN dispatch_observation s
 ON s.generation_id=t.generation_id AND s.attempt=t.attempt AND s.phase='started'
 WHERE t.generation_id=(SELECT generation_id FROM first_generation) AND t.phase IN ('first_text','first_tool_call') AND t.occurred_at>=s.occurred_at) AS first_text
 ) SELECT CASE WHEN first_text>=started THEN (first_text-started)/1000 END FROM timing`, id, id).Scan(&firstText)
	if err != nil {
		return m, err
	}
	if firstText.Valid {
		m.TTFTMS = &firstText.Int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN s.actual_micros IS NOT NULL THEN s.currency ELSE e.currency END AS currency,sum(coalesce(s.actual_micros,e.estimated_micros)),max(s.actual_micros IS NULL AND e.generation_id IS NOT NULL),coalesce(group_concat(DISTINCT CASE WHEN s.actual_micros IS NULL THEN e.source||' · '||json_extract(e.price_json,'$.version') END),'')`+scope+` AND coalesce(s.actual_micros,e.estimated_micros) IS NOT NULL GROUP BY 1 ORDER BY 1 LIMIT 20`, id, id)
	// Parenthesize scope for the cost filter (Main's own rows obey it too).
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var c observability.Cost
		if err = rows.Scan(&c.Currency, &c.Micros, &c.Estimated, &c.Source); err != nil {
			return m, err
		}
		m.Costs = append(m.Costs, c)
	}
	return m, rows.Err()
}

func scopeObservationsSQL() string {
	return `WITH members AS (SELECT * FROM run WHERE (run_id=? OR root_run_id=?) AND agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)), observations AS (` + strings.Join(observationSources, " UNION ALL ") + `) `
}

func scanObservation(row scanner) (observability.Observation, error) {
	var o observability.Observation
	var step, end sql.NullInt64
	err := row.Scan(&o.ID, &o.Parent, &o.Type, &o.Name, &o.RunID, &o.Key, &step, &o.Status, &o.Started, &end, &o.Timing, &o.Detail)
	if step.Valid {
		o.Step = &step.Int64
	}
	if end.Valid {
		o.Ended = &end.Int64
		if o.Timing == "observed wait" {
			o.Status = "resumed"
		}
	}
	return o, err
}

func (s Inspector) Observations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, error) {
	out := observability.ObservationTree{Items: []observability.Observation{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Trace, err = traceRoot(ctx, tx, q.RunID)
	if err != nil {
		return out, err
	}
	id := out.Trace.ID
	out.Trace.Metrics, err = traceMetrics(ctx, tx, id)
	if err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, scopeObservationsSQL()+`SELECT count(*),coalesce(sum(status IN ('failed','http_error')),0) FROM observations`, id, id).Scan(&out.Total, &out.Errors)
	if err != nil {
		return out, err
	}
	c, err := readCursor(q.Cursor, "observations:"+id)
	if err != nil {
		return out, err
	}
	query := scopeObservationsSQL() + `SELECT * FROM observations`
	args := []any{id, id}
	if c.ID != "" {
		query += ` WHERE (started,id)>(?,?)`
		args = append(args, c.At, c.ID)
	}
	query += ` ORDER BY started,id LIMIT 101`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		o, e := scanObservation(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if len(out.Items) == 100 {
			last := out.Items[99]
			out.Next = nextCursor("observations:"+id, last.Started, last.ID)
			break
		}
		out.Items = append(out.Items, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if q.Selected == "" {
		q.Selected = "agent:" + q.RunID
	}
	out.Selected, err = scanObservation(tx.QueryRowContext(ctx, scopeObservationsSQL()+`SELECT * FROM observations WHERE id=?`, id, id, q.Selected))
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	if err == nil && out.Selected.Type == "generation" {
		out.ProviderTimings, err = providerTimings(ctx, tx, out.Selected.Key)
		if err == nil {
			out.Generation, err = generationDetails(ctx, tx, out.Selected.Key)
		}
	}
	return out, err
}

func providerTimings(ctx context.Context, tx *sql.Tx, generationID string) ([]observability.ProviderTiming, error) {
	rows, err := tx.QueryContext(ctx, `SELECT attempt,phase,occurred_at,http_status,error_code FROM dispatch_observation
 WHERE generation_id=? ORDER BY occurred_at, CASE phase
 WHEN 'started' THEN 1 WHEN 'headers' THEN 2 WHEN 'first_body' THEN 3
 WHEN 'first_text' THEN 4 WHEN 'first_tool_call' THEN 4
 WHEN 'stream_terminal' THEN 5 WHEN 'error' THEN 7 ELSE 6 END,attempt LIMIT 32`, generationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var timings []observability.ProviderTiming
	for rows.Next() {
		var timing observability.ProviderTiming
		if err = rows.Scan(&timing.Attempt, &timing.Phase, &timing.At, &timing.HTTPStatus, &timing.ErrorCode); err != nil {
			return nil, err
		}
		timings = append(timings, timing)
	}
	return timings, rows.Err()
}

func parseStamp(raw string) (int64, error) {
	at, err := time.Parse(time.RFC3339Nano, raw)
	return at.UnixMicro(), err
}

// Keep histogram bins readable and bounded across short and long ranges.
func activityInterval(span int64) int64 {
	for _, seconds := range []int64{1, 5, 10, 30, 60, 300, 600, 1800, 3600, 10800, 21600, 43200, 86400, 604800, 2592000, 31536000} {
		step := seconds * 1_000_000
		if step >= (span+46)/47 {
			return step
		}
	}
	return max(int64(1_000_000), (span+46)/47)
}
