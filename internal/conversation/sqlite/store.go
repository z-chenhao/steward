package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/recall/sqliteindex"
	_ "modernc.org/sqlite"
)

type Store struct {
	ownerID     string
	db          *sql.DB
	engine      recall.Embedding
	localSearch bool
}

const SchemaVersion = 3

func Open(ctx context.Context, root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, conversation.ErrStorage
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, conversation.ErrStorage
	}
	path := filepath.Join(root, "conversation.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, conversation.ErrStorage
	}
	if f.Close() != nil {
		return nil, conversation.ErrStorage
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	for _, p := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, conversation.ErrStorage
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return conversation.ErrStorage
	}
	defer tx.Rollback()
	var version int
	if tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version) != nil || version < 0 || version > SchemaVersion {
		return conversation.ErrStorage
	}
	if version == 0 {
		_, err = tx.ExecContext(ctx, `CREATE TABLE conversation_message (
message_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, sequence INTEGER NOT NULL,
role TEXT NOT NULL CHECK(role IN ('user','assistant')), content_json TEXT NOT NULL CHECK(json_valid(content_json)),
source TEXT NOT NULL, source_event_id TEXT, created_at INTEGER NOT NULL,
UNIQUE(conversation_id,sequence)) STRICT;
CREATE TRIGGER conversation_no_update BEFORE UPDATE ON conversation_message BEGIN SELECT RAISE(ABORT,'immutable message'); END;
CREATE TRIGGER conversation_no_delete BEFORE DELETE ON conversation_message BEGIN SELECT RAISE(ABORT,'immutable message'); END;
CREATE TRIGGER conversation_no_replace BEFORE INSERT ON conversation_message WHEN EXISTS(SELECT 1 FROM conversation_message WHERE message_id=NEW.message_id) BEGIN SELECT RAISE(ABORT,'immutable message'); END;
PRAGMA user_version=1;`)
		if err != nil {
			return conversation.ErrStorage
		}
	}
	if version < 2 {
		if _, err = tx.ExecContext(ctx, sqliteindex.Schema+`CREATE TABLE conversation_scope(conversation_id TEXT PRIMARY KEY,user_id TEXT NOT NULL,agent_id TEXT NOT NULL) STRICT;
INSERT INTO conversation_scope SELECT DISTINCT conversation_id,'local',conversation_id FROM conversation_message;
CREATE TRIGGER conversation_scope_no_update BEFORE UPDATE ON conversation_scope BEGIN SELECT RAISE(ABORT,'immutable ownership'); END;
CREATE TRIGGER conversation_scope_no_delete BEFORE DELETE ON conversation_scope BEGIN SELECT RAISE(ABORT,'retain ownership'); END;
CREATE TRIGGER conversation_scope_no_replace BEFORE INSERT ON conversation_scope WHEN EXISTS(SELECT 1 FROM conversation_scope WHERE conversation_id=new.conversation_id) BEGIN SELECT RAISE(ABORT,'immutable ownership'); END;`); err != nil {
			return conversation.ErrStorage
		}
		rows, err := tx.QueryContext(ctx, `SELECT message_id,conversation_id,role,content_json,created_at FROM conversation_message ORDER BY conversation_id,sequence`)
		if err != nil {
			return conversation.ErrStorage
		}
		var docs []sqliteindex.Document
		for rows.Next() {
			var d sqliteindex.Document
			var raw string
			d.UserID = s.owner()
			d.Active = true
			if rows.Scan(&d.Ref, &d.AgentID, &d.AuthorRole, &raw, &d.OccurredAt) != nil {
				rows.Close()
				return conversation.ErrStorage
			}
			var parts []conversation.ContentPart
			if json.Unmarshal([]byte(raw), &parts) != nil {
				rows.Close()
				return conversation.ErrStorage
			}
			d.Content = textContent(parts)
			d.Ref = "conversation:" + d.Ref
			docs = append(docs, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return conversation.ErrStorage
		}
		for _, d := range docs {
			if sqliteindex.Insert(ctx, tx, d) != nil {
				return conversation.ErrStorage
			}
		}
		if _, err = tx.ExecContext(ctx, `PRAGMA user_version=2`); err != nil {
			return conversation.ErrStorage
		}
	}
	if version < 3 {
		if _, err = tx.ExecContext(ctx, compactionSchema+`PRAGMA user_version=3;`); err != nil {
			return conversation.ErrStorage
		}
	}
	if tx.Commit() != nil {
		return conversation.ErrStorage
	}
	return nil
}

// Append atomically assigns monotonic sequences in supplied intent order.
// Repeating a previously committed prefix is harmless; changed IDs fail closed.
func (s *Store) Append(ctx context.Context, messages []conversation.Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return conversation.ErrStorage
	}
	defer tx.Rollback()
	for _, m := range messages {
		parts, err := conversation.Normalize(m.Content)
		if err != nil || m.MessageID == "" || m.ConversationID == "" || (m.Role != "user" && m.Role != "assistant") || m.Source == "" || m.CreatedAt.IsZero() {
			return conversation.ErrInvalid
		}
		for _, p := range parts {
			if p.Type == "tool_call" {
				return conversation.ErrInvalid
			}
		}
		data, err := llm.CanonicalJSON(parts)
		if err != nil {
			return conversation.ErrInvalid
		}
		var c, role, content, source, event string
		var created int64
		err = tx.QueryRowContext(ctx, `SELECT conversation_id,role,content_json,source,coalesce(source_event_id,''),created_at FROM conversation_message WHERE message_id=?`, m.MessageID).Scan(&c, &role, &content, &source, &event, &created)
		if err == nil {
			if c != m.ConversationID || role != m.Role || content != string(data) || source != m.Source || event != m.SourceEventID || created != m.CreatedAt.UnixMicro() {
				return conversation.ErrConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return conversation.ErrStorage
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO conversation_message VALUES(?,?,(SELECT coalesce(max(sequence),0)+1 FROM conversation_message WHERE conversation_id=?),?,?,?,?,?)`, m.MessageID, m.ConversationID, m.ConversationID, m.Role, string(data), m.Source, m.SourceEventID, m.CreatedAt.UnixMicro())
		if err != nil {
			return conversation.ErrStorage
		}
		// Legacy local Conversation IDs are Agent identities. Other channels
		// register their authenticated ownership before appending messages.
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversation_scope SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM conversation_scope WHERE conversation_id=?)`, m.ConversationID, s.owner(), m.ConversationID, m.ConversationID); err != nil {
			return conversation.ErrStorage
		}
		d := sqliteindex.Document{Ref: "conversation:" + m.MessageID, Content: textContent(parts), OccurredAt: m.CreatedAt.UnixMicro(), AuthorRole: m.Role, Active: true}
		if tx.QueryRowContext(ctx, `SELECT user_id,agent_id FROM conversation_scope WHERE conversation_id=?`, m.ConversationID).Scan(&d.UserID, &d.AgentID) != nil || sqliteindex.Insert(ctx, tx, d) != nil {
			return conversation.ErrStorage
		}
	}
	if tx.Commit() != nil {
		return conversation.ErrStorage
	}
	return nil
}

func (s *Store) List(ctx context.Context, id string) ([]conversation.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id,sequence,role,content_json,source,coalesce(source_event_id,''),created_at FROM conversation_message WHERE conversation_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, conversation.ErrStorage
	}
	return readMessages(rows, id)
}

func readMessages(rows *sql.Rows, id string) ([]conversation.Message, error) {
	defer rows.Close()
	result := []conversation.Message{}
	for rows.Next() {
		var m conversation.Message
		var raw string
		var created int64
		m.ConversationID = id
		if rows.Scan(&m.MessageID, &m.Sequence, &m.Role, &raw, &m.Source, &m.SourceEventID, &created) != nil || json.Unmarshal([]byte(raw), &m.Content) != nil {
			return nil, conversation.ErrStorage
		}
		m.CreatedAt = time.UnixMicro(created).UTC()
		result = append(result, m)
	}
	if rows.Err() != nil {
		return nil, conversation.ErrStorage
	}
	return result, nil
}
func (s *Store) Close() error { return s.db.Close() }

func textContent(parts []conversation.ContentPart) string {
	var text []string
	for _, p := range parts {
		if p.Type == "text" {
			text = append(text, p.Text)
		}
	}
	return strings.Join(text, "\n")
}
func (s *Store) Configure(ctx context.Context, e recall.Embedding) error {
	if err := sqliteindex.Bind(ctx, s.db, e); err != nil {
		return err
	}
	s.engine = e
	s.localSearch = false
	return nil
}
func (s *Store) ConfigureLocal() { s.localSearch = true }
func (s *Store) Search(ctx context.Context, scope recall.Scope, q recall.Query) ([]recall.Hit, error) {
	if s.localSearch {
		return sqliteindex.SearchLocal(ctx, s.db, scope, q, "conversation")
	}
	return sqliteindex.Search(ctx, s.db, scope, q, s.engine, "conversation")
}

// BindConversation freezes ownership. Rebinding an existing Conversation fails.
func (s *Store) BindConversation(ctx context.Context, id string, scope recall.Scope) error {
	if id == "" || scope.UserID == "" || scope.AgentID == "" {
		return recall.ErrInvalid
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO conversation_scope SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM conversation_scope WHERE conversation_id=?)`, id, scope.UserID, scope.AgentID, id); err != nil {
		return conversation.ErrStorage
	}
	var user, agent string
	if s.db.QueryRowContext(ctx, `SELECT user_id,agent_id FROM conversation_scope WHERE conversation_id=?`, id).Scan(&user, &agent) != nil {
		return conversation.ErrStorage
	}
	if user != scope.UserID || agent != scope.AgentID {
		return recall.ErrConflict
	}
	return nil
}

// Get selects one immutable message under both conversation and message IDs.
func (s *Store) Get(ctx context.Context, id, messageID string) (conversation.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id,sequence,role,content_json,source,coalesce(source_event_id,''),created_at FROM conversation_message WHERE conversation_id=? AND message_id=?`, id, messageID)
	if err != nil {
		return conversation.Message{}, conversation.ErrStorage
	}
	messages, err := readMessages(rows, id)
	if err != nil {
		return conversation.Message{}, err
	}
	if len(messages) != 1 {
		return conversation.Message{}, conversation.ErrConflict
	}
	return messages[0], nil
}

// GetScoped reads exactly the same ownership domain as Search; a reference is
// a selector, never proof of ownership. The binding and message are immutable.
func (s *Store) GetScoped(ctx context.Context, scope recall.Scope, messageID string) (conversation.Message, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT m.conversation_id FROM conversation_message m JOIN conversation_scope c USING(conversation_id) WHERE m.message_id=? AND c.user_id=? AND (c.agent_id='' OR c.agent_id=?)`, messageID, scope.UserID, scope.AgentID).Scan(&id)
	if err == sql.ErrNoRows {
		return conversation.Message{}, conversation.ErrConflict
	}
	if err != nil {
		return conversation.Message{}, conversation.ErrStorage
	}
	return s.Get(ctx, id, messageID)
}

// EvidenceSourceVisible checks an exact source without reading its content.
// A missing message permits the Host to check an accepted local intent.
// An existing foreign ownership binding denies that fallback.
func (s *Store) EvidenceSourceVisible(ctx context.Context, scope recall.Scope, id, messageID string, floor int64) (bool, error) {
	var owner sql.NullBool
	var created sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT
 (SELECT user_id=? AND agent_id=? FROM conversation_scope WHERE conversation_id=?),
 (SELECT created_at FROM conversation_message WHERE conversation_id=? AND message_id=?)`,
		scope.UserID, scope.AgentID, id, id, messageID).Scan(&owner, &created)
	if err != nil {
		return false, conversation.ErrStorage
	}
	if owner.Valid && !owner.Bool {
		return false, nil
	}
	if !created.Valid {
		return false, conversation.ErrConflict
	}
	return owner.Bool && created.Int64 > floor, nil
}

func (s *Store) Page(ctx context.Context, id string, before int64) (conversation.Page, error) {
	if before < 0 {
		return conversation.Page{}, conversation.ErrInvalid
	}
	const pageQuery = `SELECT message_id,sequence,role,content_json,source,coalesce(source_event_id,''),created_at FROM conversation_message WHERE conversation_id=? AND (?=0 OR sequence<?) ORDER BY sequence DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, pageQuery, id, before, before, conversation.PageSize+1)
	if err != nil {
		return conversation.Page{}, conversation.ErrStorage
	}
	messages, err := readMessages(rows, id)
	if err != nil {
		return conversation.Page{}, err
	}
	page := conversation.Page{}
	if len(messages) > conversation.PageSize {
		messages = messages[:conversation.PageSize]
		page.Before = messages[len(messages)-1].Sequence
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	page.Messages = messages
	return page, nil
}

// GetMany loads admitted immutable IDs in bounded batches without scanning
// unrelated history. The result retains sequence order across all batches.
func (s *Store) GetMany(ctx context.Context, id string, ids []string) ([]conversation.Message, error) {
	result := make([]conversation.Message, 0, len(ids))
	for start := 0; start < len(ids); start += 128 {
		batch := ids[start:min(start+128, len(ids))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, id)
		for _, messageID := range batch {
			args = append(args, messageID)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT message_id,sequence,role,content_json,source,coalesce(source_event_id,''),created_at FROM conversation_message WHERE conversation_id=? AND message_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, conversation.ErrStorage
		}
		messages, err := readMessages(rows, id)
		if err != nil {
			return nil, err
		}
		if len(messages) != len(batch) {
			return nil, conversation.ErrConflict
		}
		result = append(result, messages...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	return result, nil
}

func (s *Store) owner() string {
	if s.ownerID != "" {
		return s.ownerID
	}
	return artifact.LocalUser
}

// BindOwner runs before serving or starting background workers. Existing
// conversation ownership is validated, never rewritten.
func (s *Store) BindOwner(ctx context.Context, owner, agent string) error {
	var foreign int
	if owner == "" {
		owner = artifact.LocalUser
	}
	if s.ownerID != "" && s.ownerID != owner {
		return conversation.ErrConflict
	}
	if e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conversation_scope WHERE conversation_id=? AND user_id<>?`, agent, owner).Scan(&foreign); e != nil {
		return e
	}
	if foreign != 0 {
		return conversation.ErrConflict
	}
	s.ownerID = owner
	return nil
}
