package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

const currentMemoryRows = ` FROM memory_record m LEFT JOIN recall_document d ON d.ref='memory:'||m.memory_id
 WHERE m.user_id=? AND ((m.scope_type='user' AND m.scope_id=?) OR (m.scope_type='agent' AND m.scope_id=?))
 AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM memory_retirement retired WHERE retired.memory_id=m.memory_id) AND NOT EXISTS(SELECT 1 FROM memory_semantic_record semantic WHERE semantic.memory_id=m.memory_id AND semantic.expires_at>0 AND semantic.expires_at<=unixepoch('subsec')*1000000) AND (d.active=1 OR m.content LIKE 'sealed:v1:%')
 AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id) AND NOT EXISTS(SELECT 1 FROM memory_document_dependency dep JOIN memory_record source ON source.memory_id=dep.source_id WHERE dep.memory_id=m.memory_id AND (source.deleted_at IS NOT NULL OR EXISTS(SELECT 1 FROM memory_record successor WHERE successor.supersedes_memory_id=source.memory_id) OR EXISTS(SELECT 1 FROM memory_retirement retired WHERE retired.memory_id=source.memory_id)))`

// Evidence is an in-process exact read and the identity needed to recheck it.
type Evidence struct {
	Text      string
	Refs      []string
	CreatedAt time.Time
	scope     recall.Scope
	id        string
	floor     int64
	stored    string
}

// ReadEvidence selects one current record with at most 21 explicit source
// candidates. The SQL snapshot closes before any SecretStore access.
func (s *Store) ReadEvidence(ctx context.Context, scope recall.Scope, id string, floor int64, linksOffset int, vault memory.Vault) (Evidence, error) {
	const refs = `(SELECT json_group_array(ref) FROM (
 SELECT DISTINCT source.ref FROM (
 SELECT json_extract(m.source_refs,j.fullkey||'.source_ref') AS ref,
        json_extract(m.source_refs,j.fullkey||'.source_type') AS kind
 FROM json_each(m.source_refs) j
 ) source
 WHERE source.kind IN ('conversation','task') AND length(CAST(source.ref AS BLOB))<=256
 AND substr(source.ref,1,length(source.kind)+1)=source.kind||':'
 ORDER BY ref LIMIT 21 OFFSET ?))`
	var record memory.Record
	var at int64
	var sourceJSON string
	err := s.db.QueryRowContext(ctx, `SELECT m.memory_id,m.user_id,m.scope_type,m.scope_id,m.content,m.created_at,`+refs+currentMemoryRows+
		` AND m.memory_id=? AND m.created_at>?`, linksOffset, scope.UserID, scope.UserID, scope.AgentID, id, floor).
		Scan(&record.ID, &record.UserID, &record.ScopeType, &record.ScopeID, &record.Content, &at, &sourceJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, recall.ErrConflict
	}
	if err != nil {
		return Evidence{}, recall.ErrUnavailable
	}
	result := Evidence{CreatedAt: time.UnixMicro(at).UTC(), scope: scope, id: id, floor: floor, stored: record.Content}
	if json.Unmarshal([]byte(sourceJSON), &result.Refs) != nil {
		return Evidence{}, recall.ErrUnavailable
	}
	result.Text = record.Content
	// Reserved sealed representations never fall back to literal plaintext.
	if strings.EqualFold(record.Content[:min(len(record.Content), len("sealed:"))], "sealed:") {
		result.Text, err = vault.Open(ctx, record.Content, binding(record))
		if err != nil {
			return Evidence{}, recall.ErrUnavailable
		}
	}
	return result, nil
}

// RecheckEvidence also rejects a representation change during the read, for
// example concurrent protection of a legacy plaintext record.
func (s *Store) RecheckEvidence(ctx context.Context, v Evidence) error {
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT m.content`+currentMemoryRows+` AND m.memory_id=? AND m.created_at>?`,
		v.scope.UserID, v.scope.UserID, v.scope.AgentID, v.id, v.floor).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return recall.ErrConflict
	}
	if err != nil {
		return recall.ErrUnavailable
	}
	if stored != v.stored {
		return recall.ErrConflict
	}
	return nil
}
