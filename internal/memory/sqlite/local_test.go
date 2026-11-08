package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm/memory"
	mem "github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/recall/fake"
)

func TestLocalSearchEncryptedMemoryRestartScopeAndForget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	secrets := memory.New()
	if err := secrets.Put(ctx, "credential:key", []byte(strings.Repeat("k", 32))); err != nil {
		t.Fatal(err)
	}
	open := func() *Store {
		s, err := Open(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ConfigureVault(mem.Vault{Secrets: secrets, Ref: "credential:key"}); err != nil {
			t.Fatal(err)
		}
		s.ConfigureLocal()
		return s
	}
	s := open()
	scope := recall.Scope{UserID: "local", AgentID: "one"}
	text := "我喜欢喝无糖绿茶"
	if err := s.Curate(ctx, "remember", scope, mem.SourceRef{Type: "feedback", Ref: "feedback:one"}, time.Unix(100, 0), []string{text}, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	hits, err := s.Search(ctx, scope, recall.Query{Query: "无糖绿茶"})
	if err != nil || len(hits) != 1 || hits[0].Content != text {
		t.Fatal(hits, err)
	}
	hits, err = s.Search(ctx, recall.Scope{UserID: "other", AgentID: "one"}, recall.Query{Query: "无糖绿茶"})
	if err != nil || len(hits) != 0 {
		t.Fatal("scope leak", hits, err)
	}
	var bindings int
	if err = s.db.QueryRow(`SELECT count(*) FROM recall_configuration`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("local search bound a vector space", err)
	}
	for _, name := range []string{"memory.db", "memory.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(root, name))
		if strings.Contains(string(raw), text) {
			t.Fatal("plaintext persisted")
		}
	}
	if err = s.Forget(ctx, scope, "remember-0", time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	hits, err = s.Search(ctx, scope, recall.Query{Query: "无糖绿茶"})
	if err != nil || len(hits) != 0 {
		t.Fatal("forgotten result", hits, err)
	}
}

func TestRecallExcludesDerivedWorkingFactsBeforeCandidateLimit(t *testing.T) {
	for _, local := range []bool{true, false} {
		t.Run(fmt.Sprint("local=", local), func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if local {
				s.ConfigureLocal()
			} else if err = s.Configure(ctx, fake.Embedding{}); err != nil {
				t.Fatal(err)
			}
			scope := recall.Scope{UserID: "local", AgentID: "one"}
			for i := 0; i < 65; i++ {
				source := "conversation:working"
				if i == 0 {
					source = "conversation:older"
				}
				err = s.Apply(ctx, mem.Record{ID: fmt.Sprintf("fact-%03d", i), UserID: "local", ScopeType: "agent", ScopeID: "one", Tier: "recall", Content: fmt.Sprintf("tea fact %d", i), CreatedAt: time.Unix(100+int64(i), 0), SourceRefs: []mem.SourceRef{{Type: "conversation", Ref: source}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			hits, err := s.Search(ctx, scope, recall.Query{Query: "tea", Limit: 1, ExcludeRefs: []string{"conversation:working"}})
			if err != nil || len(hits) != 1 || hits[0].SourceRef != "memory:fact-000" {
				t.Fatal("derived echoes starved missing fact", hits, err)
			}
			visible := []string{}
			for i := 1; i < 65; i++ {
				visible = append(visible, fmt.Sprintf("memory:fact-%03d", i))
			}
			hits, err = s.Search(ctx, scope, recall.Query{Query: "tea", Limit: 1, SeenRefs: visible})
			if err != nil || len(hits) != 1 || hits[0].SourceRef != "memory:fact-000" {
				t.Fatal("seen references consumed candidate limit", hits, err)
			}
			hits, err = s.Search(ctx, scope, recall.Query{Query: "tea", Limit: 50, SeenRefs: []string{"conversation:working"}})
			if err != nil || len(hits) < 2 {
				t.Fatal("seen source transitively hid unread memory", hits, err)
			}

		})
	}
}
