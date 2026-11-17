package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"strconv"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (s Inspector) traceManaged(ctx context.Context, tx *sql.Tx) (bool, error) {
	// Activation history is authoritative; a missing in-memory Catalog cannot
	// turn an already managed reader into a never-managed embedding.
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('rsi_activation','rsi_graph','rsi_package')`).Scan(&tables); err != nil {
		return false, err
	}
	if tables == 0 {
		return false, nil
	} // Explicit pre-plugin/standalone fact readers.
	if tables != 3 {
		return false, rsi.ErrInvalid
	}
	var managed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') s WHERE json_extract(s.value,'$.contract')=?)`, contract.Contract).Scan(&managed)
	return managed, err
}

func (s Inspector) pluginDiff(ctx context.Context, tx *sql.Tx, q observability.DiffQuery, previous int64) (observability.ContextDiff, error) {
	out := observability.ContextDiff{Previous: &previous, Items: []observability.ContextChange{}}
	read := func(step int64) ([]contract.Record, error) {
		rows, err := tx.QueryContext(ctx, `SELECT cast(j.key AS integer),coalesce(json_extract(j.value,'$.role'),json_extract(j.value,'$.name'),'record'),coalesce(nullif(json_extract(j.value,'$.source.ref'),''),''),coalesce(nullif(json_extract(j.value,'$.tool_call_id'),''),''),json_extract(j.value,'$.name'),typeof(j.value),j.value FROM model_generation g,json_each(`+contextSection(q.Section)+`) j WHERE g.run_id=? AND g.step_index=? ORDER BY cast(j.key AS integer)`, q.RunID, step)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		records := []contract.Record{}
		for rows.Next() {
			var r contract.Record
			var name, value sql.NullString
			var kind string
			if err = rows.Scan(&r.Index, &r.Label, &r.SourceRef, &r.CallID, &name, &kind, &value); err != nil {
				return nil, err
			}
			if name.Valid {
				r.ToolName = &name.String
			}
			r.ValueKnown = value.Valid
			r.Digest = valueFingerprint(kind, value.String)
			records = append(records, r)
		}
		return records, rows.Err()
	}
	before, err := read(previous)
	if err != nil {
		return out, err
	}
	after, err := read(q.Step)
	if err != nil {
		return out, err
	}
	facts := contract.DiffFacts{Section: q.Section, After: q.After, Before: before, AfterRecords: after}
	data, err := json.Marshal(facts)
	if err != nil {
		return out, err
	}
	raw, revision, managed, err := s.traceService(ctx, tx, contract.DiffContract, data, q.ReaderRevision)
	if err != nil {
		return out, err
	}
	if !managed {
		return out, rsi.ErrRevoked
	}
	var diff contract.Diff
	if err = rsiplugin.DecodeResult(raw, &diff); err != nil {
		return out, err
	}
	if len(diff.Items) > observability.ContextPage || diff.Next != nil && (*diff.Next != q.After+len(diff.Items) || len(diff.Items) != observability.ContextPage) {
		return out, rsi.ErrInvalid
	}
	old, newRecords := map[int]contract.Record{}, map[int]contract.Record{}
	for _, r := range before {
		old[r.Index] = r
	}
	for _, r := range after {
		newRecords[r.Index] = r
	}
	for _, c := range diff.Items {
		label := ""
		if c.Before != nil {
			r, ok := old[*c.Before]
			if !ok {
				return out, rsi.ErrInvalid
			}
			label = r.Label
		}
		if c.After != nil {
			r, ok := newRecords[*c.After]
			if !ok {
				return out, rsi.ErrInvalid
			}
			label = r.Label
		}
		if c.Before == nil && c.After == nil {
			return out, rsi.ErrInvalid
		}
		out.Items = append(out.Items, observability.ContextChange{Kind: c.Kind, Before: c.Before, After: c.After, Label: label})
	}
	out.Next = diff.Next
	out.ReaderRevision = revision
	return out, nil
}

// SQLite compares INTEGER/REAL numerically and TEXT byte-for-byte. Preserve
// that existing equality while transferring only fingerprints to the plugin.
func valueFingerprint(kind, value string) string {
	prefix := kind + ":"
	if kind == "integer" {
		if n, ok := new(big.Rat).SetString(value); ok {
			prefix = "number:"
			value = n.String()
		}
	}
	if kind == "real" {
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			if exact := new(big.Rat).SetFloat64(n); exact != nil {
				prefix = "number:"
				value = exact.String()
			}
		}
	}
	return rsiplugin.FileDigest([]byte(prefix + value))
}
