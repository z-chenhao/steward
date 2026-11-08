package sqliteindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/recall"
)

// SearchLocal uses the existing text index without binding or changing a vector
// space. Turning semantic search on later can safely index the same originals.
func SearchLocal(ctx context.Context, db *sql.DB, scope recall.Scope, q recall.Query, source string) ([]recall.Hit, error) {
	if !q.Valid() || scope.UserID == "" || scope.AgentID == "" {
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
	terms := recall.Terms(q.Query)
	out := []recall.Hit{}
	if len(terms) == 0 {
		return out, nil
	}
	conditions := []string{}
	excluded, _ := json.Marshal(append(append([]string{}, q.ExcludeRefs...), q.SeenRefs...))
	args := []any{scope.UserID, scope.AgentID, start, end, string(excluded)}
	for _, term := range terms {
		conditions = append(conditions, "instr(lower(d.content),?)>0")
		args = append(args, term)
	}
	// Scope/time/visibility filter every candidate. Rank by actual query
	// coverage before Core/recency, rather than recency of a common OR term.
	rows, err := db.QueryContext(ctx, `SELECT d.ref,d.content,d.occurred_at,d.author_role,d.core FROM recall_document d
WHERE d.user_id=? AND (d.agent_id='' OR d.agent_id=?) AND d.active=1 AND d.occurred_at>=? AND d.occurred_at<=?
AND d.ref NOT IN (SELECT value FROM json_each(?))
AND (`+strings.Join(conditions, " OR ")+`)`, args...)
	if err != nil {
		return nil, recall.ErrUnavailable
	}
	defer rows.Close()
	less := func(i, j int) bool {
		if out[i].Relevance != out[j].Relevance {
			return out[i].Relevance > out[j].Relevance
		}
		if !out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].OccurredAt.After(out[j].OccurredAt)
		}
		return out[i].SourceRef < out[j].SourceRef
	}
	for rows.Next() {
		var h recall.Hit
		var at int64
		var core bool
		if rows.Scan(&h.SourceRef, &h.Content, &at, &h.AuthorRole, &core) != nil {
			return nil, recall.ErrUnavailable
		}
		h.SourceType, h.OccurredAt = source, time.UnixMicro(at).UTC()
		h.Relevance = recall.Relevance(terms, h.Content)
		if h.Relevance >= .25 && recall.QuotedMatch(q.Query, h.Content) {
			if core {
				h.Relevance += .001
			}
			out = append(out, h)
			if len(out) >= 400 {
				sort.SliceStable(out, less)
				out = out[:200]
			}
		}
	}
	if rows.Err() != nil {
		return nil, recall.ErrUnavailable
	}
	sort.SliceStable(out, less)
	if len(out) > q.Count() {
		out = out[:q.Count()]
	}
	return out, nil
}
