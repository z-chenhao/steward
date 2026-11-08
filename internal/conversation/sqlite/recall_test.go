package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
	"github.com/z-chenhao/assistant/internal/recall/fake"
)

type appendDuringQuery struct {
	append func() error
	err    error
}

func TestRecallExcludesWorkingMessagesBeforeCandidateLimit(t *testing.T) {
	for _, local := range []bool{true, false} {
		t.Run(fmt.Sprint("local=", local), func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			scope := recall.Scope{UserID: "local", AgentID: "one"}
			if err = s.BindConversation(ctx, "one", scope); err != nil {
				t.Fatal(err)
			}
			messages := []conversation.Message{{MessageID: "older", ConversationID: "one", Role: "user", Content: conversation.Text("tea older missing evidence"), Source: "main", CreatedAt: time.Unix(1, 0)}}
			q := recall.Query{Query: "tea", Limit: 1}
			for i := 0; i < 210; i++ {
				id := fmt.Sprintf("visible-%03d", i)
				messages = append(messages, conversation.Message{MessageID: id, ConversationID: "one", Role: "user", Content: conversation.Text("tea"), Source: "main", CreatedAt: time.Unix(100+int64(i), 0)})
				q.ExcludeRefs = append(q.ExcludeRefs, "conversation:"+id)
			}
			if err = s.Append(ctx, messages); err != nil {
				t.Fatal(err)
			}
			if local {
				s.ConfigureLocal()
			} else if err = s.Configure(ctx, fake.Embedding{}); err != nil {
				t.Fatal(err)
			}
			hits, err := s.Search(ctx, scope, q)
			if err != nil || len(hits) != 1 || hits[0].SourceRef != "conversation:older" {
				t.Fatal("visible history starved missing evidence", hits, err)
			}
			if !local {
				var pending int
				if err = s.db.QueryRow(`SELECT count(*) FROM recall_document WHERE vector_json IS NULL`).Scan(&pending); err != nil || pending != 210 {
					t.Fatal("working messages were needlessly embedded", pending, err)
				}
			}
		})
	}
}

func (*appendDuringQuery) Identity() string { return "append-during-query" }
func (e *appendDuringQuery) Embed(_ context.Context, text string) ([]float64, error) {
	if text == "query-boundary" && e.append != nil {
		append := e.append
		e.append = nil
		e.err = append()
	}
	return []float64{1, 0}, e.err
}

// A Run may append its reply while another Run is searching. Force the exact
// catch-up/query boundary without sleeps, scheduler luck or a provider outage.
func TestRecallConcurrentPendingAppendFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scope := recall.Scope{UserID: "local", AgentID: "steward"}
	if err = s.BindConversation(ctx, "steward", scope); err != nil {
		t.Fatal(err)
	}
	appendMessage := func(id string) error {
		return s.Append(ctx, []conversation.Message{{MessageID: id, ConversationID: "steward",
			Role: "assistant", Content: conversation.Text("tea " + id), Source: "main",
			CreatedAt: time.Unix(1, 0)}})
	}
	if err = appendMessage("original"); err != nil {
		t.Fatal(err)
	}
	e := &appendDuringQuery{append: func() error { return appendMessage("concurrent") }}
	if err = s.Configure(ctx, e); err != nil {
		t.Fatal(err)
	}
	query := recall.Query{Query: "query-boundary"}
	hits, err := s.Search(ctx, scope, query)
	if e.err != nil {
		t.Fatal("append failed", e.err)
	}
	if err != recall.ErrUnavailable || hits != nil {
		t.Fatal("pending concurrent evidence admitted a partial search", hits, err)
	}
	var pending int
	if err = s.db.QueryRow(`SELECT count(*) FROM recall_document WHERE vector_json IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("did not force pending append", pending, err)
	}
	hits, err = s.Search(ctx, scope, query)
	if err != nil || len(hits) != 2 {
		t.Fatal("later explicit caller could not recover both originals", hits, err)
	}
	originals, err := s.List(ctx, "steward")
	if err != nil || len(originals) != 2 || originals[0].MessageID != "original" || originals[1].MessageID != "concurrent" {
		t.Fatal("immutable originals changed", originals, err)
	}
}

// Use the real HTTP adapter: the semantic unit Fake does not enforce its bound.
func TestMultipartHistoryWithHTTPEmbedding(t *testing.T) {
	for _, state := range []string{"fresh", "prior-v1", "pending-v2", "max-parts"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			parts := []conversation.ContentPart{
				{Type: "text", Text: strings.Repeat("a", 40000)},
				{Type: "text", Text: strings.Repeat("茶", 13330) + " tailmarker"},
			}
			if state == "max-parts" {
				parts = make([]conversation.ContentPart, 128)
				for i := range parts {
					parts[i] = conversation.ContentPart{Type: "text", Text: strings.Repeat("a", 64<<10)}
				}
			}
			original, err := json.Marshal(parts)
			if err != nil {
				t.Fatal(err)
			}
			if state == "prior-v1" {
				db, err := sql.Open("sqlite", filepath.Join(root, "conversation.db"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`CREATE TABLE conversation_message(message_id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,sequence INTEGER NOT NULL,role TEXT NOT NULL,content_json TEXT NOT NULL,source TEXT NOT NULL,source_event_id TEXT,created_at INTEGER NOT NULL,UNIQUE(conversation_id,sequence)) STRICT;
PRAGMA user_version=1;`)
				if err == nil {
					_, err = db.Exec(`INSERT INTO conversation_message VALUES('multipart','steward',1,'user',?,'cli','event',1000000)`, string(original))
				}
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			s, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			if state != "prior-v1" {
				err = s.Append(ctx, []conversation.Message{{MessageID: "multipart", ConversationID: "steward", Role: "user", Content: parts, Source: "cli", SourceEventID: "event", CreatedAt: time.Unix(1, 0)}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if state == "pending-v2" {
				// A document persisted by the reviewed implementation stays pending
				// across reopen; fixing it must not require rewriting any original.
				s.Close()
				s, err = Open(ctx, root)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.List(ctx, "steward")
			if err != nil {
				t.Fatal(err)
			}
			var originalJSON string
			if err = s.db.QueryRow(`SELECT content_json FROM conversation_message WHERE message_id='multipart'`).Scan(&originalJSON); err != nil {
				t.Fatal(err)
			}
			if state == "prior-v1" && originalJSON != string(original) {
				t.Fatal("migration rewrote original JSON")
			}
			var requests []string
			var mu sync.Mutex
			failAt := 2
			takeRequests := func() []string {
				mu.Lock()
				defer mu.Unlock()
				out := requests
				requests = nil
				failAt = 0
				return out
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Input string `json:"input"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
					w.WriteHeader(400)
					return
				}
				if !utf8.ValidString(body.Input) || len(body.Input) > 64<<10 {
					t.Error("invalid embedding chunk")
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				requests = append(requests, body.Input)
				fail := len(requests) == failAt
				mu.Unlock()
				if fail {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, `{"model":"test","data":[{"index":0,"embedding":[1,0]}]}`)
			}))
			defer server.Close()
			client := embedding.Client{Config: embedding.Config{Provider: "openai_compatible", Endpoint: server.URL, Model: "test", Dimensions: 2}}
			if err = s.Configure(ctx, client); err != nil {
				t.Fatal(err)
			}
			scope := recall.Scope{UserID: "local", AgentID: "steward"}
			query := recall.Query{Query: "tailmarker"}
			if hits, err := s.Search(ctx, scope, query); err == nil || hits != nil {
				t.Fatal("partial indexing admitted recall", hits, err)
			}
			var vector sql.NullString
			if err = s.db.QueryRow(`SELECT vector_json FROM recall_document WHERE ref='conversation:multipart'`).Scan(&vector); err != nil || vector.Valid {
				t.Fatal("partial vector committed", vector, err)
			}
			if len(takeRequests()) != 2 {
				t.Fatal("did not reach second chunk failure")
			}
			hits, err := s.Search(ctx, scope, query)
			joined := textContent(parts)
			if err != nil || len(hits) != 1 || hits[0].Content != joined || hits[0].SourceRef != "conversation:multipart" || hits[0].AuthorRole != "user" {
				t.Fatal("valid multipart unavailable", len(hits), err)
			}
			got := takeRequests()
			if len(got) < 3 || got[len(got)-1] != query.Query || strings.Join(got[:len(got)-1], "") != joined {
				t.Fatal("content truncated or query chunked", len(got))
			}
			_, err = s.Search(ctx, scope, query)
			if got = takeRequests(); err != nil || !reflect.DeepEqual(got, []string{query.Query}) {
				t.Fatal("completed vector not reused", got, err)
			}
			after, err := s.List(ctx, "steward")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("original rewritten", err)
			}
			var raw string
			if err = s.db.QueryRow(`SELECT content_json FROM conversation_message WHERE message_id='multipart'`).Scan(&raw); err != nil || raw != originalJSON {
				t.Fatal("original JSON rewritten", err)
			}
		})
	}
}

func TestPriorConversationMigrationPreservesOriginalAndHybridRecall(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(root, "conversation.db"))
	if e != nil {
		t.Fatal(e)
	}
	// Exact M1 table columns; migration must not rewrite the original JSON or ID.
	_, e = db.Exec(`CREATE TABLE conversation_message(message_id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,sequence INTEGER NOT NULL,role TEXT NOT NULL,content_json TEXT NOT NULL,source TEXT NOT NULL,source_event_id TEXT,created_at INTEGER NOT NULL,UNIQUE(conversation_id,sequence)) STRICT;
INSERT INTO conversation_message VALUES('original','steward',1,'user','[{"type":"text","text":"chai preference"}]','cli','event',1000000);PRAGMA user_version=1;`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Configure(ctx, fake.Embedding{}); e != nil {
		t.Fatal(e)
	}
	before, e := s.List(ctx, "steward")
	if e != nil {
		t.Fatal(e)
	}
	h, e := s.Search(ctx, recall.Scope{UserID: "local", AgentID: "steward"}, recall.Query{Query: "tea"})
	if e != nil || len(h) != 1 || h[0].SourceRef != "conversation:original" || h[0].AuthorRole != "user" || !h[0].OccurredAt.Equal(time.Unix(1, 0)) {
		t.Fatal(h, e)
	}
	after, _ := s.List(ctx, "steward")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("original rewritten")
	}
	var raw string
	s.db.QueryRow(`SELECT content_json FROM conversation_message`).Scan(&raw)
	if raw != `[{"type":"text","text":"chai preference"}]` {
		t.Fatal(raw)
	}
	if e = s.BindConversation(ctx, "different-channel", recall.Scope{UserID: "local", AgentID: "steward"}); e != nil {
		t.Fatal(e)
	}
	if e = s.BindConversation(ctx, "foreign", recall.Scope{UserID: "bob", AgentID: "steward"}); e != nil {
		t.Fatal(e)
	}
	for _, c := range []string{"different-channel", "foreign", "other-agent"} {
		if e = s.Append(ctx, []conversation.Message{{MessageID: c, ConversationID: c, Role: "assistant", Content: conversation.Text("tea evidence " + c), Source: "main", CreatedAt: time.Unix(2, 0)}}); e != nil {
			t.Fatal(e)
		}
	}
	h, e = s.Search(ctx, recall.Scope{UserID: "local", AgentID: "steward"}, recall.Query{Query: "tea"})
	if e != nil || len(h) != 2 || h[0].SourceRef != "conversation:different-channel" {
		t.Fatal(h, e)
	}
	if e = s.BindConversation(ctx, "different-channel", recall.Scope{UserID: "bob", AgentID: "steward"}); e != recall.ErrConflict {
		t.Fatal("rebound ownership", e)
	}
	for _, statement := range []string{`UPDATE conversation_scope SET user_id='bob'`, `DELETE FROM conversation_scope`, `INSERT OR REPLACE INTO conversation_scope VALUES('different-channel','bob','steward')`} {
		if _, e = s.db.Exec(statement); e == nil {
			t.Fatal("mutable persisted ownership", statement)
		}
	}
	if e = s.migrate(ctx); e != nil {
		t.Fatal("repeat migration", e)
	}
}
func TestConversationIndexRollbackAndFaultFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Configure(ctx, fake.Embedding{})
	s.db.Exec(`CREATE TRIGGER index_fault BEFORE INSERT ON recall_document BEGIN SELECT RAISE(ABORT,'fault'); END`)
	m := conversation.Message{MessageID: "m", ConversationID: "steward", Role: "user", Content: conversation.Text("tea"), Source: "cli", CreatedAt: time.Now()}
	if e = s.Append(ctx, []conversation.Message{m}); e == nil {
		t.Fatal("partial append")
	}
	history, _ := s.List(ctx, "steward")
	if len(history) != 0 {
		t.Fatal(history)
	}
	s.db.Exec(`DROP TRIGGER index_fault`)
	if e = s.Append(ctx, []conversation.Message{m}); e != nil {
		t.Fatal(e)
	}
	s.db.Exec(`DROP TABLE recall_fts`)
	h, e := s.Search(ctx, recall.Scope{UserID: "local", AgentID: "steward"}, recall.Query{Query: "tea"})
	if e == nil || h != nil {
		t.Fatal("vector-only degraded success", h, e)
	}
}

type rejectEmptyEmbedding struct{ fake.Embedding }

func (e rejectEmptyEmbedding) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, recall.ErrUnavailable
	}
	return e.Embedding.Embed(ctx, text)
}
func TestMediaOnlyOriginalDoesNotBlockTextRecall(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Configure(ctx, rejectEmptyEmbedding{})
	media := conversation.Message{MessageID: "media", ConversationID: "steward", Role: "user", Source: "cli", CreatedAt: time.Unix(1, 0), Content: []conversation.ContentPart{{Type: "image", ArtifactRef: "artifact:opaque"}}}
	text := media
	text.MessageID = "text"
	text.Content = conversation.Text("chai preference")
	if e = s.Append(ctx, []conversation.Message{media, text}); e != nil {
		t.Fatal(e)
	}
	h, e := s.Search(ctx, recall.Scope{UserID: "local", AgentID: "steward"}, recall.Query{Query: "tea"})
	if e != nil || len(h) != 1 || h[0].SourceRef != "conversation:text" {
		t.Fatal(h, e)
	}
	original, e := s.List(ctx, "steward")
	if e != nil || len(original) != 2 || original[0].Content[0].ArtifactRef != "artifact:opaque" {
		t.Fatal(original, e)
	}
}

func TestMemoryHostOwnerPreservesForeignChannelsWithoutRebindingThem(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.BindConversation(ctx, "other-channel", recall.Scope{UserID: "bob", AgentID: "other-agent"}); e != nil {
		t.Fatal(e)
	}
	if e = s.BindOwner(ctx, "alice", "agent-a"); e != nil {
		t.Fatal("foreign channel prevented scoped runtime", e)
	}
	if e = s.BindOwner(ctx, "", "agent-a"); e == nil {
		t.Fatal("implicit local owner replaced bound identity")
	}
	if e = s.BindOwner(ctx, "bob", "other-agent"); e == nil {
		t.Fatal("in-process owner switched")
	}
	if e = s.BindConversation(ctx, "agent-a", recall.Scope{UserID: "bob", AgentID: "agent-a"}); e != nil {
		t.Fatal(e)
	}
	if e = s.BindOwner(ctx, "alice", "agent-a"); e == nil {
		t.Fatal("main conversation rebound")
	}
}
