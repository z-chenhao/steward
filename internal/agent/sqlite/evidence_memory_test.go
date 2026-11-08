package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/recall"
)

func evidenceMemoryVault(t *testing.T, s *Store) (*memorysqlite.Store, memory.Vault) {
	t.Helper()
	ctx := context.Background()
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	secrets := secretmemory.New()
	if err = secrets.Put(ctx, "memory-evidence-test", bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	vault := memory.Vault{Secrets: secrets, Ref: "memory-evidence-test"}
	if err = m.ConfigureVault(vault); err != nil {
		t.Fatal(err)
	}
	return m, vault
}

func evidenceFact(id, owner string, refs ...memory.SourceRef) memory.Record {
	if len(refs) == 0 {
		refs = []memory.SourceRef{{Type: "feedback", Ref: "feedback:private"}}
	}
	return memory.Record{ID: id, UserID: artifact.LocalUser, ScopeType: "agent", ScopeID: owner, Tier: "recall", Content: "Current exact evidence", SourceRefs: refs, CreatedAt: time.Now()}
}

func TestMemoryEvidenceExactScopeSourcesAndPagesWithoutWriter(t *testing.T) {
	ctx := context.Background()
	s, r, history, a, _ := loopFixture(t)
	m, vault := evidenceMemoryVault(t, s)
	owner := idAgent(t, s)
	// Keep the input in state.db only. Reading its link must never flush it.
	input := mustSubmit(t, a, "memory-source", "Owner source before history flush")
	inputRef := "conversation:" + input.MessageID
	refs := []memory.SourceRef{
		{Type: "conversation", Ref: inputRef},
		{Type: "conversation", Ref: "conversation:foreign"},
		{Type: "run", Ref: "conversation:visible-00"},
		{Type: "task", Ref: "conversation:visible-00"},
		{Type: "feedback", Ref: "feedback:internal"},
	}
	for i := range 22 {
		id := fmt.Sprintf("visible-%02d", i)
		if err := history.Append(ctx, []conversation.Message{{MessageID: id, ConversationID: owner, Role: "user", Content: conversation.Text("Explicit source"), Source: "cli", CreatedAt: time.Now()}}); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, memory.SourceRef{Type: "conversation", Ref: "conversation:" + id})
	}
	if err := history.Append(ctx, []conversation.Message{{MessageID: "foreign", ConversationID: "another-conversation", Role: "user", Content: conversation.Text("Other scope"), Source: "cli", CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	target := evidenceFact("current", owner, refs...)
	otherUser := evidenceFact("other-user", owner)
	otherUser.UserID = "another-user"
	otherAgent := evidenceFact("other-agent", "another-agent")
	legacyUserScope := evidenceFact("user-scope", owner)
	legacyUserScope.ScopeType, legacyUserScope.ScopeID = "user", artifact.LocalUser
	for _, fact := range []memory.Record{target, otherUser, otherAgent, legacyUserScope, evidenceFact("deleted", owner), evidenceFact("replaced", owner)} {
		if err := m.Apply(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Forget(ctx, recall.Scope{UserID: artifact.LocalUser, AgentID: owner}, "deleted", time.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := evidenceFact("replacement", owner)
	replacement.Supersedes = "replaced"
	if err := m.Apply(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", "other-user", "other-agent", "deleted", "replaced"} {
		page, err := r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:" + id}, m, vault)
		if !errors.Is(err, artifact.ErrNotFound) || page.Ref != "" {
			t.Fatal("hidden record differed from a missing reference", id, err)
		}
	}
	if _, err := r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:user-scope"}, m, vault); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	first, err := r.ReadMemoryEvidence(readCtx, evidence.Request{Ref: "memory:current"}, m, vault)
	if err != nil || first.Text != target.Content || first.NextLinksOffset == nil || *first.NextLinksOffset != 20 || len(first.Links) != 19 || !hasEvidenceLink(first, inputRef) {
		t.Fatal("owner source page failed or competed for the WAL writer", first, err)
	}
	last, err := r.ReadMemoryEvidence(readCtx, evidence.Request{Ref: first.Ref, LinksOffset: *first.NextLinksOffset}, m, vault)
	if err != nil || len(last.Links) != 4 || last.NextLinksOffset != nil {
		t.Fatal(last, err)
	}
	if _, err = history.Get(ctx, owner, input.MessageID); !errors.Is(err, conversation.ErrConflict) {
		t.Fatal("owner evidence read flushed the accepted source", err)
	}
}

func TestMemoryEvidenceSourceRejectsForeignOwnerEvenWithLocalIntent(t *testing.T) {
	ctx := context.Background()
	s, r, history, a, _ := loopFixture(t)
	m, vault := evidenceMemoryVault(t, s)
	owner := idAgent(t, s)
	input := mustSubmit(t, a, "conflicting-owner", "Accepted local intent")
	if err := history.BindConversation(ctx, owner, recall.Scope{UserID: "another-user", AgentID: owner}); err != nil {
		t.Fatal(err)
	}
	if err := history.Append(ctx, []conversation.Message{{MessageID: "private-source", ConversationID: owner, Role: "user", Content: conversation.Text("Different user"), Source: "cli", CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	fact := evidenceFact("owner-fact", owner,
		memory.SourceRef{Type: "conversation", Ref: "conversation:private-source"},
		memory.SourceRef{Type: "conversation", Ref: "conversation:" + input.MessageID})
	if err := m.Apply(ctx, fact); err != nil {
		t.Fatal(err)
	}
	page, err := r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:owner-fact"}, m, vault)
	if err != nil || page.Text != fact.Content || len(page.Links) != 0 {
		t.Fatal("foreign scope or fallback was visible", page, err)
	}
}

type blockedEvidenceSecret struct {
	llm.SecretStore
	started chan struct{}
	resume  chan struct{}
}

func (b blockedEvidenceSecret) Get(ctx context.Context, ref string) ([]byte, error) {
	close(b.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.resume:
		return b.SecretStore.Get(ctx, ref)
	}
}

func TestMemoryEvidenceRechecksForgetAndCorrectionAfterDecryption(t *testing.T) {
	for _, mutation := range []string{"privacy barrier", "correction", "tombstone"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			s, r, history, a, _ := loopFixture(t)
			m, vault := evidenceMemoryVault(t, s)
			owner := idAgent(t, s)
			input := mustSubmit(t, a, "old-memory-source", "Old source before the privacy barrier")
			fact := evidenceFact("current", owner, memory.SourceRef{Type: "conversation", Ref: "conversation:" + input.MessageID})
			if err := m.Apply(ctx, fact); err != nil {
				t.Fatal(err)
			}
			// The deadline bounds the decryption/revocation handshake, not
			// schema setup under the race detector's admitted CPU budget.
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			b := blockedEvidenceSecret{SecretStore: vault.Secrets, started: make(chan struct{}), resume: make(chan struct{})}
			vault.Secrets = b
			done := make(chan struct{})
			var page evidence.Page
			var readErr error
			go func() {
				defer close(done)
				page, readErr = r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:current"}, m, vault)
			}()
			defer func() { cancel(); <-done }()
			select {
			case <-b.started:
			case <-ctx.Done():
				t.Fatal("read never entered the SecretStore")
			}
			writeCtx, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			var err error
			switch mutation {
			case "privacy barrier":
				err = s.Transaction(writeCtx, func(tx *sql.Tx) error { return r.revokeMemory(writeCtx, tx, owner) })
			case "correction":
				replacement := evidenceFact("corrected", owner)
				replacement.Supersedes = fact.ID
				err = m.Apply(writeCtx, replacement)
			case "tombstone":
				err = m.Forget(writeCtx, recall.Scope{UserID: artifact.LocalUser, AgentID: owner}, fact.ID, time.Now())
			}
			if err != nil {
				t.Fatal("SecretStore wait prevented revocation from committing", err)
			}
			close(b.resume)
			<-done
			if !errors.Is(readErr, artifact.ErrNotFound) || page.Ref != "" {
				t.Error("revoked record became readable", readErr)
			}
			if mutation == "privacy barrier" {
				vault.Secrets = b.SecretStore
				if _, err = r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:current"}, m, vault); !errors.Is(err, artifact.ErrNotFound) {
					t.Fatal("record predating Forget remained visible", err)
				}
				fresh := evidenceFact("fresh", owner, fact.SourceRefs[0], memory.SourceRef{Type: "conversation", Ref: "conversation:new-source"})
				if err = history.Append(ctx, []conversation.Message{{MessageID: "new-source", ConversationID: owner, Role: "user", Content: conversation.Text("Current source"), Source: "cli", CreatedAt: fresh.CreatedAt}}); err != nil {
					t.Fatal(err)
				}
				if err = m.Apply(ctx, fresh); err != nil {
					t.Fatal(err)
				}
				current, err := r.ReadMemoryEvidence(ctx, evidence.Request{Ref: "memory:fresh"}, m, vault)
				if err != nil || current.Text != fresh.Content || len(current.Links) != 1 || current.Links[0].Ref != "conversation:new-source" {
					t.Fatal("fresh record did not filter a pre-Forget link", current, err)
				}
			}
		})
	}
}
