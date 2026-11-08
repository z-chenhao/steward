// Package sqliteindex is the selected private SQLite FTS5 / exact cosine adapter.
// Provider interfaces expose neither this index nor its internal ranking scores.
package sqliteindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
)

const Schema = `CREATE TABLE recall_document (
 ref TEXT PRIMARY KEY, user_id TEXT NOT NULL, agent_id TEXT NOT NULL,
 content TEXT NOT NULL, occurred_at INTEGER NOT NULL, author_role TEXT NOT NULL,
 core INTEGER NOT NULL CHECK(core IN(0,1)), active INTEGER NOT NULL CHECK(active IN(0,1)),
 vector_json TEXT CHECK(vector_json IS NULL OR json_valid(vector_json)), embedding_id TEXT
) STRICT;
CREATE INDEX recall_scope ON recall_document(user_id,agent_id,active,occurred_at);
CREATE TABLE recall_configuration(singleton INTEGER PRIMARY KEY CHECK(singleton=1), embedding_id TEXT NOT NULL) STRICT;
CREATE VIRTUAL TABLE recall_fts USING fts5(content,content='recall_document',content_rowid='rowid',tokenize='unicode61');
CREATE TRIGGER recall_insert AFTER INSERT ON recall_document BEGIN INSERT INTO recall_fts(rowid,content) VALUES(new.rowid,new.content); END;
CREATE TRIGGER recall_delete AFTER DELETE ON recall_document BEGIN INSERT INTO recall_fts(recall_fts,rowid,content) VALUES('delete',old.rowid,old.content); END;
CREATE TRIGGER recall_update AFTER UPDATE OF content ON recall_document BEGIN
 INSERT INTO recall_fts(recall_fts,rowid,content) VALUES('delete',old.rowid,old.content);
 INSERT INTO recall_fts(rowid,content) VALUES(new.rowid,new.content); END;`

type Document struct {
	Ref, UserID, AgentID, Content, AuthorRole string
	OccurredAt                                int64
	Core, Active                              bool
}

func Insert(ctx context.Context, tx *sql.Tx, d Document) error {
	// Media-only Conversation entries retain their immutable originals and
	// references, but have no text to embed or match in this text recall index.
	d.Active = d.Active && strings.TrimSpace(d.Content) != ""
	_, e := tx.ExecContext(ctx, `INSERT INTO recall_document(ref,user_id,agent_id,content,occurred_at,author_role,core,active) VALUES(?,?,?,?,?,?,?,?)`, d.Ref, d.UserID, d.AgentID, d.Content, d.OccurredAt, d.AuthorRole, d.Core, d.Active)
	return e
}

// Bind rejects model/dimension/endpoint drift rather than mixing vector spaces.
// Changing implementations requires the documented offline index rebuild.
func Bind(ctx context.Context, db *sql.DB, e recall.Embedding) error {
	if e == nil || e.Identity() == "" {
		return recall.ErrUnavailable
	}
	var existing string
	err := db.QueryRowContext(ctx, `SELECT embedding_id FROM recall_configuration WHERE singleton=1`).Scan(&existing)
	if err == nil {
		if existing != e.Identity() {
			return recall.ErrConflict
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return recall.ErrUnavailable
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO recall_configuration VALUES(1,?) ON CONFLICT DO NOTHING`, e.Identity()); err != nil {
		return recall.ErrUnavailable
	}
	var id string
	if db.QueryRowContext(ctx, `SELECT embedding_id FROM recall_configuration WHERE singleton=1`).Scan(&id) != nil || id != e.Identity() {
		return recall.ErrConflict
	}
	return nil
}

// Indexing is recoverable derived work: immutable originals commit first. A
// failed embedding leaves NULL vectors and fails the complete search closed.
// Only authorized records are ever sent to the configured embedding endpoint.
func catchUp(ctx context.Context, db *sql.DB, s recall.Scope, e recall.Embedding, start, end int64, excluded string) error {
	if err := Bind(ctx, db, e); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT ref,content FROM recall_document WHERE user_id=? AND (agent_id='' OR agent_id=?) AND active=1 AND vector_json IS NULL AND occurred_at>=? AND occurred_at<=? AND ref NOT IN (SELECT value FROM json_each(?)) ORDER BY ref`, s.UserID, s.AgentID, start, end, excluded)
	if err != nil {
		return recall.ErrUnavailable
	}
	type missing struct{ ref, text string }
	var pending []missing
	for rows.Next() {
		var m missing
		if rows.Scan(&m.ref, &m.text) != nil {
			rows.Close()
			return recall.ErrUnavailable
		}
		pending = append(pending, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return recall.ErrUnavailable
	}
	for _, m := range pending {
		v, err := documentVector(ctx, e, m.text)
		if err != nil || !embedding.Valid(v) {
			return recall.ErrUnavailable
		}
		raw, _ := json.Marshal(v)
		if _, err = db.ExecContext(ctx, `UPDATE recall_document SET vector_json=?,embedding_id=? WHERE ref=? AND vector_json IS NULL AND active=1`, string(raw), e.Identity(), m.ref); err != nil {
			return recall.ErrUnavailable
		}
	}
	return nil
}
func words(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for i, p := range parts {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(parts, " OR ")
}
func cosine(a, b []float64) (float64, error) {
	if len(a) != len(b) || !embedding.Valid(b) {
		return 0, recall.ErrUnavailable
	}
	dot, aa, bb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb)), nil
}

type candidate struct {
	d     Document
	v     []float64
	score float64
}

func Search(ctx context.Context, db *sql.DB, s recall.Scope, q recall.Query, e recall.Embedding, source string) ([]recall.Hit, error) {
	if !q.Valid() || s.UserID == "" || s.AgentID == "" {
		return nil, recall.ErrInvalid
	}
	start, end := int64(math.MinInt64), int64(math.MaxInt64)
	if q.TimeRange != nil {
		if q.TimeRange.Start != nil {
			start = q.TimeRange.Start.UnixMicro()
		}
		if q.TimeRange.End != nil {
			end = q.TimeRange.End.UnixMicro()
		}
	}
	excluded, _ := json.Marshal(append(append([]string{}, q.ExcludeRefs...), q.SeenRefs...))
	if err := catchUp(ctx, db, s, e, start, end, string(excluded)); err != nil {
		return nil, err
	}
	vector, err := e.Embed(ctx, q.Query)
	if err != nil || !embedding.Valid(vector) {
		return nil, recall.ErrUnavailable
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT ref,content,occurred_at,author_role,core,vector_json,embedding_id FROM recall_document WHERE user_id=? AND (agent_id='' OR agent_id=?) AND active=1 AND occurred_at>=? AND occurred_at<=? AND ref NOT IN (SELECT value FROM json_each(?)) ORDER BY ref`, s.UserID, s.AgentID, start, end, string(excluded))
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	var all []candidate
	for rows.Next() {
		var c candidate
		var raw, id sql.NullString
		if rows.Scan(&c.d.Ref, &c.d.Content, &c.d.OccurredAt, &c.d.AuthorRole, &c.d.Core, &raw, &id) != nil || !raw.Valid || id.String != e.Identity() || json.Unmarshal([]byte(raw.String), &c.v) != nil {
			rows.Close()
			return nil, recall.ErrUnavailable
		}
		all = append(all, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	type ranks struct {
		refs []string
		err  error
	}
	ch := make(chan ranks, 1)
	go func() {
		var result ranks
		term := words(q.Query)
		if term != "" {
			rows, err := tx.QueryContext(ctx, `SELECT d.ref FROM recall_fts JOIN recall_document d ON d.rowid=recall_fts.rowid WHERE recall_fts MATCH ? AND d.user_id=? AND (d.agent_id='' OR d.agent_id=?) AND d.active=1 AND d.occurred_at>=? AND d.occurred_at<=? AND d.ref NOT IN (SELECT value FROM json_each(?)) ORDER BY bm25(recall_fts),d.ref LIMIT 200`, term, s.UserID, s.AgentID, start, end, string(excluded))
			if err != nil {
				result.err = err
			} else {
				for rows.Next() {
					var ref string
					if err = rows.Scan(&ref); err != nil {
						break
					}
					result.refs = append(result.refs, ref)
				}
				result.err = err
				if result.err == nil {
					result.err = rows.Err()
				}
				rows.Close()
			}
		}
		ch <- result
	}()
	var vectors []candidate
	for _, c := range all {
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
		score, e := cosine(vector, c.v)
		if e != nil {
			err = e
			break
		}
		if score >= 0.25 {
			c.score = score
			vectors = append(vectors, c)
		}
	}
	fts := <-ch
	if err != nil || fts.err != nil {
		return nil, recall.ErrUnavailable
	}
	if tx.Commit() != nil {
		return nil, recall.ErrUnavailable
	}
	sort.Slice(vectors, func(i, j int) bool {
		if vectors[i].score != vectors[j].score {
			return vectors[i].score > vectors[j].score
		}
		return vectors[i].d.Ref < vectors[j].d.Ref
	})
	if len(vectors) > 200 {
		vectors = vectors[:200]
	}
	// Reciprocal Rank Fusion k=60; domain-specific tie breaking below.
	scores := map[string]float64{}
	for i, ref := range fts.refs {
		scores[ref] += 1 / float64(61+i)
	}
	for i, c := range vectors {
		scores[c.d.Ref] += 1 / float64(61+i)
	}
	var fused []candidate
	for _, c := range all {
		if score, ok := scores[c.d.Ref]; ok {
			c.score = score
			fused = append(fused, c)
		}
	}
	sort.Slice(fused, func(i, j int) bool {
		a, b := fused[i], fused[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if source == "memory" && a.d.Core != b.d.Core {
			return a.d.Core
		}
		if a.d.OccurredAt != b.d.OccurredAt {
			return a.d.OccurredAt > b.d.OccurredAt
		}
		return a.d.Ref < b.d.Ref
	})
	if len(fused) > q.Count() {
		fused = fused[:q.Count()]
	}
	out := []recall.Hit{}
	for _, c := range fused {
		out = append(out, recall.Hit{Content: c.d.Content, SourceType: source, SourceRef: c.d.Ref, OccurredAt: time.UnixMicro(c.d.OccurredAt).UTC(), AuthorRole: c.d.AuthorRole})
	}
	return out, nil
}
