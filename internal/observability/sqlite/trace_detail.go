package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/observability"
)

func selectedObservation(ctx context.Context, tx *sql.Tx, q observability.ObservationQuery) (observability.Observation, error) {
	t, err := traceRoot(ctx, tx, q.RunID)
	if err != nil {
		return observability.Observation{}, err
	}
	o, err := scanObservation(tx.QueryRowContext(ctx, scopeObservationsSQL()+`SELECT * FROM observations WHERE id=?`, t.ID, t.ID, q.Selected))
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	return o, err
}

func (s Inspector) ObservationBody(ctx context.Context, q observability.BodyQuery) (observability.Body, error) {
	if err := q.Validate(); err != nil {
		return observability.Body{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.Body{}, err
	}
	defer tx.Rollback()
	o, err := selectedObservation(ctx, tx, q.ObservationQuery)
	if err != nil {
		return observability.Body{}, err
	}
	selection := ""
	args := []any{o.Key}
	note := ""
	switch o.Type {
	case "generation":
		switch q.Field {
		case "input":
			selection = `SELECT json_extract(request_json,'$.messages') FROM model_generation WHERE generation_id=?`
			note = "Frozen Host input; before Bridge/provider conversion"
		case "output":
			selection = `SELECT CASE WHEN status='completed' THEN result_json END FROM model_generation WHERE generation_id=?`
			note = "Accepted model result; missing output is unknown"
		case "wire":
			selection = `SELECT CASE WHEN count(*)>0 THEN json_group_array(json_object('attempt',attempt,'format',format,'body',json(body_json),'body_hash',body_hash,'omitted',omitted,'at',occurred_at)) END FROM (SELECT * FROM dispatch_observation WHERE generation_id=? AND phase='started' ORDER BY occurred_at,attempt)`
			note = "Sanitized final HTTP request(s); headers excluded, image pixels replaced. Missing capture does not prove no dispatch."
		}
	case "tool":
		if q.Field == "input" {
			selection = `SELECT arguments_json FROM tool_invocation WHERE invocation_id=?`
		} else if q.Field == "output" {
			selection = `SELECT coalesce(result_json,error_json) FROM tool_invocation WHERE invocation_id=?`
		}
		note = "Saved tool input/result; unknown effects are never retried by inspection"
	case "agent":
		if q.Field == "input" {
			selection = `SELECT json_group_array(json(message_json)) FROM main_input WHERE run_id=?`
			if o.Name != "Main" {
				selection = `SELECT CASE WHEN count(*)>0 THEN json_group_array(json(j.value)) END FROM model_generation g,json_each(g.request_json,'$.messages') j WHERE g.generation_id=(SELECT generation_id FROM model_generation WHERE run_id=? ORDER BY created_at,generation_id LIMIT 1) AND json_extract(j.value,'$.source.kind')='task'`
			}
		} else if q.Field == "output" {
			selection = `SELECT result_json FROM model_generation g WHERE run_id=? AND status='completed' AND NOT EXISTS(SELECT 1 FROM context_compaction c WHERE c.run_id=g.run_id AND c.step_index=g.step_index AND json_extract(c.provenance_json,'$.stage')='summary_request') ORDER BY step_index DESC LIMIT 1`
		}
		note = "Saved incoming messages / last accepted model result; not a delivery receipt"
		if o.Name != "Main" && q.Field == "input" {
			note = "Original task brief from the first frozen model request; current task edits are not substituted"
		}
	case "retriever":
		args = []any{o.RunID, o.Step}
		if q.Field == "input" {
			selection = `SELECT json_object('query',query) FROM run_recall WHERE run_id=? AND step_index=?`
		} else if q.Field == "output" {
			selection = `SELECT hits_json FROM run_recall WHERE run_id=? AND step_index=?`
		}
		note = "Frozen retrieval; current Memory is not queried"
	case "chain":
		args = []any{o.RunID, o.Step}
		if q.Field == "input" {
			selection = `SELECT provenance_json FROM context_compaction WHERE run_id=? AND step_index=?`
		} else if q.Field == "output" {
			selection = `SELECT messages_json FROM context_compaction WHERE run_id=? AND step_index=?`
		}
		note = "Saved context preparation and omission references"
	case "span":
		switch o.Name {
		case "Provider HTTP":
			if q.Field == "wire" || q.Field == "input" {
				selection = `SELECT body_json FROM dispatch_observation WHERE attempt=? AND generation_id IN (SELECT generation_id FROM model_generation WHERE run_id=?) AND phase='started'`
				args = append(args, o.RunID)
			}
			note = "Final SDK body, sanitized before storage. Timing measures fetch to response headers, not model inference."
		case "Wait for spend approval":
			selection = `SELECT json_object('status',status,'created_at',created_at,'decided_at',decided_at) FROM spend_approval WHERE approval_id=?`
			note = "Frozen monetary authorization interval; not a model dispatch"
		case "Wait for approval":
			selection = `SELECT json_object('status',status,'risk',risk_type,'created_at',created_at,'decided_at',decided_at) FROM approval WHERE approval_id=?`
			note = "Approval interval; not provider time"
		default:
			if q.Field != "input" {
				return observability.Body{Note: "No per-segment output saved; inspect the parent Tool for its accepted result"}, nil
			}
			selection = `SELECT json_object('kind',json_extract(event_json,'$.kind'),'reason',json_extract(event_json,'$.text'),'step',json_extract(event_json,'$.step_index')) FROM run_trace WHERE sequence=? AND run_id=?`
			args = append(args, o.RunID)
			note = "Best-effort execution/wait evidence; missing intervals remain unknown"
		}
	case "event":
		selection = `SELECT json_object('kind',json_extract(event_json,'$.kind'),'step',json_extract(event_json,'$.step_index')) FROM run_trace WHERE sequence=? AND run_id=?`
		args = append(args, o.RunID)
		note = "Recorded event; result reuse never adds usage or an external call"
	}
	if selection == "" {
		return observability.Body{Note: "No saved body for this field"}, nil
	}
	out, err := readBody(ctx, tx, selection, args, q.Offset)
	out.Note = note
	return out, err
}

func (s Inspector) ChatPreview(ctx context.Context, q observability.ObservationQuery) ([]observability.ChatPreview, error) {
	out := []observability.ChatPreview{}
	if err := q.Validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	o, err := selectedObservation(ctx, tx, q)
	if err != nil {
		return out, err
	}
	if o.Type != "generation" {
		return out, observability.ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT cast(j.key AS integer),json_extract(j.value,'$.role'),substr(`+messageTextSQL+`,1,1400),coalesce(json_extract(j.value,'$.source.kind'),''),coalesce(json_extract(j.value,'$.tool_call_id'),''),substr(coalesce(json_extract(j.value,'$.tool_calls'),''),1,1000),length(`+messageTextSQL+`)>1400 FROM model_generation g,json_each(g.request_json,'$.messages') j WHERE g.generation_id=? ORDER BY cast(j.key AS integer) LIMIT 20`, o.Key)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m observability.ChatPreview
		if err = rows.Scan(&m.Index, &m.Role, &m.Text, &m.Source, &m.CallID, &m.Calls, &m.More); err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
