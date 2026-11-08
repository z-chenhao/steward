package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/z-chenhao/assistant/internal/observability"
)

func (s Inspector) Rounds(ctx context.Context, q observability.RoundQuery) (observability.Rounds, error) {
	out := observability.Rounds{Items: []observability.Round{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	// Check scope even if this Run has no model generation yet.
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM run WHERE run_id=? AND agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, q.RunID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT generation_id,step_index,status,error_code,created_at FROM model_generation WHERE run_id=? AND step_index>? ORDER BY step_index LIMIT ?`, q.RunID, q.After, observability.ContextPage+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r observability.Round
		var at int64
		if err = rows.Scan(&r.ID, &r.Step, &r.Status, &r.Error, &at); err != nil {
			return out, err
		}
		if len(out.Items) == observability.ContextPage {
			step := out.Items[len(out.Items)-1].Step
			out.Next = &step
			break
		}
		r.CreatedAt = stamp(at)
		out.Items = append(out.Items, r)
	}
	return out, rows.Err()
}

func frozenRound(ctx context.Context, tx *sql.Tx, run string, step int64) (observability.FrozenContext, error) {
	out := observability.FrozenContext{Items: []observability.ContextItem{}}
	var at int64
	var previous sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT g.generation_id,g.step_index,g.status,g.error_code,g.created_at,g.request_hash,(SELECT max(p.step_index) FROM model_generation p WHERE p.run_id=g.run_id AND p.step_index<g.step_index) FROM model_generation g JOIN run r USING(run_id) WHERE g.run_id=? AND g.step_index=? AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, run, step).Scan(&out.Round.ID, &out.Round.Step, &out.Round.Status, &out.Round.Error, &at, &out.RequestHash, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		err = observability.ErrNotFound
	}
	if previous.Valid {
		out.Previous = &previous.Int64
	}
	out.Round.CreatedAt = stamp(at)
	return out, err
}

// The section expressions are fixed projections: never return request_json,
// model_snapshot, checkpoint_json, owner_token, credentials or media bytes.
// json_each yields ordered stored objects; substr bounds each detail read.
func contextSection(section string) string {
	switch section {
	case "messages":
		return `json_extract(g.request_json,'$.messages')`
	case "tools":
		return `coalesce(json_extract(g.request_json,'$.tools'),'[]')`
	case "response":
		return `CASE WHEN g.status='completed' AND g.result_json IS NOT NULL THEN json_array(json(g.result_json)) ELSE '[]' END`
	case "provenance":
		return `coalesce((SELECT json_array(json(c.provenance_json)) FROM context_compaction c WHERE c.run_id=g.run_id AND c.step_index=g.step_index),'[]')`
	case "recall":
		return `coalesce((SELECT hits_json FROM run_recall c WHERE c.run_id=g.run_id AND c.step_index=coalesce((SELECT json_extract(p.provenance_json,'$.recall_step') FROM context_compaction p WHERE p.run_id=g.run_id AND p.step_index=g.step_index),g.step_index)),'[]')`
	}
	panic("validated context section")
}

const itemColumns = `cast(j.key AS integer),coalesce(json_extract(j.value,'$.role'),json_extract(j.value,'$.name'),json_extract(j.value,'$.stage'),json_extract(j.value,'$.source_type'),'record'),coalesce(json_extract(j.value,'$.source.kind'),''),coalesce(json_extract(j.value,'$.source.ref'),json_extract(j.value,'$.source_ref'),''),coalesce(json_extract(j.value,'$.tool_call_id'),'')`

// A sealed memory envelope is content, even when the persisted text is empty.
// Inspection never decrypts it or substitutes current Memory for frozen input.
const messageTextSQL = `coalesce(json_extract(j.value,'$.text'),'') || CASE WHEN json_type(j.value,'$.memory_text')='object' THEN CASE WHEN coalesce(json_extract(j.value,'$.text'),'')='' THEN '' ELSE char(10) END || '[Encrypted memory content; resolved before model transport. Not shown in this view.]' ELSE '' END`

func scanItem(row scanner) (observability.ContextItem, error) {
	var v observability.ContextItem
	err := row.Scan(&v.Index, &v.Label, &v.SourceKind, &v.SourceRef, &v.ToolCallID)
	return v, err
}

func (s Inspector) Context(ctx context.Context, q observability.ContextQuery) (observability.FrozenContext, error) {
	if err := q.Validate(); err != nil {
		return observability.FrozenContext{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.FrozenContext{}, err
	}
	defer tx.Rollback()
	out, err := frozenRound(ctx, tx, q.RunID, q.Step)
	if err != nil {
		return out, err
	}
	from := ` FROM model_generation g,json_each(` + contextSection(q.Section) + `) j WHERE g.run_id=? AND g.step_index=?`
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+from+` AND cast(j.key AS integer)>=? ORDER BY cast(j.key AS integer) LIMIT ?`, q.RunID, q.Step, q.After, observability.ContextPage+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		v, e := scanItem(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if len(out.Items) == observability.ContextPage {
			out.Next = &v.Index
			break
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	selected, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+from+` AND cast(j.key AS integer)=?`, q.RunID, q.Step, q.Item))
	if errors.Is(err, sql.ErrNoRows) {
		if q.Item == 0 && len(out.Items) == 0 && q.After == 0 && q.Offset == 0 {
			return out, nil
		}
		return out, observability.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Selected = &selected
	content := `j.value`
	if !q.JSON && (q.Section == "messages" || q.Section == "response") {
		content = messageTextSQL
	}
	// BLOB offsets preserve embedded NULs; trim only an incomplete final rune.
	// A continuation URL always starts at the resulting UTF-8 boundary.
	var body []byte
	err = tx.QueryRowContext(ctx, `SELECT substr(cast(`+content+` AS blob),?,?),length(cast(`+content+` AS blob))`+from+` AND cast(j.key AS integer)=?`, q.Offset+1, observability.ContextChunk, q.RunID, q.Step, q.Item).Scan(&body, &out.Length)
	if err != nil {
		return out, err
	}
	out.Content, out.NextOffset, err = contentChunk(body, q.Offset, out.Length)
	if err != nil {
		return out, err
	}
	return out, nil
}

// Diff only compares frozen inputs. Source refs align current-format messages;
// legacy messages without refs align by stored position, tools by name. A
// duplicate key uses its occurrence number. No heuristic text/source inference.
func (s Inspector) ContextDiff(ctx context.Context, q observability.DiffQuery) (observability.ContextDiff, error) {
	out := observability.ContextDiff{Items: []observability.ContextChange{}}
	if err := q.Validate(); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	round, err := frozenRound(ctx, tx, q.RunID, q.Step)
	if err != nil {
		return out, err
	}
	out.Previous = round.Previous
	if out.Previous == nil {
		return out, nil
	}
	key := `coalesce('ref:'||nullif(json_extract(j.value,'$.source.ref'),''),'call:'||nullif(json_extract(j.value,'$.tool_call_id'),''),'position:'||j.key)`
	if q.Section == "tools" {
		key = `'tool:'||json_extract(j.value,'$.name')`
	}
	query := `WITH records AS (
 SELECT g.step_index step,cast(j.key AS integer) idx,j.value val,` + key + ` k,
        coalesce(json_extract(j.value,'$.role'),json_extract(j.value,'$.name'),'record') label
 FROM model_generation g,json_each(` + contextSection(q.Section) + `) j
 WHERE g.run_id=? AND g.step_index IN (?,?)
), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY step,k ORDER BY idx) occurrence FROM records
), old AS (SELECT * FROM ranked WHERE step=?), new AS (SELECT * FROM ranked WHERE step=?)
SELECT old.idx,new.idx,coalesce(new.label,old.label),
 CASE WHEN old.idx IS NULL THEN 'added' WHEN new.idx IS NULL THEN 'removed'
      WHEN old.val!=new.val THEN 'changed' ELSE 'moved' END
FROM old FULL OUTER JOIN new ON old.k=new.k AND old.occurrence=new.occurrence
WHERE old.idx IS NULL OR new.idx IS NULL OR old.val!=new.val OR old.idx!=new.idx
ORDER BY coalesce(new.idx,old.idx),coalesce(new.k,old.k),coalesce(new.occurrence,old.occurrence)
LIMIT ? OFFSET ?`
	rows, err := tx.QueryContext(ctx, query, q.RunID, *out.Previous, q.Step, *out.Previous, q.Step, observability.ContextPage+1, q.After)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var before, after sql.NullInt64
		var change observability.ContextChange
		if err = rows.Scan(&before, &after, &change.Label, &change.Kind); err != nil {
			return out, err
		}
		if len(out.Items) == observability.ContextPage {
			next := q.After + observability.ContextPage
			out.Next = &next
			break
		}
		if before.Valid {
			v := int(before.Int64)
			change.Before = &v
		}
		if after.Valid {
			v := int(after.Int64)
			change.After = &v
		}
		out.Items = append(out.Items, change)
	}
	return out, rows.Err()
}
