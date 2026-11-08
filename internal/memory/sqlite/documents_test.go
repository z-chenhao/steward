package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

func TestLayeredMemoryDocumentsCASReplayEncryptionAndDependencies(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	at := time.Now()
	sources := []memory.SourceRef{{Type: "conversation", Ref: "conversation:original"}}
	write := func(event, id string, w memory.DocumentWrite) (memory.Document, error) {
		return s.WriteDocument(ctx, scope, 0, event, id, at, sources, w)
	}
	note, err := write("note", "n1", memory.DocumentWrite{Layer: memory.Notes, Title: "Private strategy title", Content: "A tentative strategy: vary timing; validate before relying on it."})
	if err != nil {
		t.Fatal(err)
	}
	w := memory.DocumentWrite{Layer: memory.Representations, Title: "Navigation wiki", Content: "A synthesized navigation strategy", Links: []string{note.Ref}}
	wiki, err := write("wiki", "w1", w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write("wiki", "ignored-on-retry", w); err != nil {
		t.Fatal("receipt replay", err)
	}
	if _, err = write("wiki", "other", memory.DocumentWrite{Layer: memory.Notes, Title: "different", Content: "different"}); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("receipt accepted different payload", err)
	}
	bootstrap, err := write("injected", "i1", memory.DocumentWrite{Layer: memory.Injected, Title: "Session memory", Content: "Use the navigation wiki for route planning.", Links: []string{wiki.Ref}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write("duplicate-injected", "i2", memory.DocumentWrite{Layer: memory.Injected, Title: "second", Content: "second"}); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("two injected heads", err)
	}
	for _, layer := range []string{memory.Notes, memory.Representations, memory.Injected} {
		docs, e := s.ListDocuments(ctx, scope, 0, layer, 20)
		if e != nil || len(docs) != 1 || docs[0].Layer != layer {
			t.Fatal(layer, docs, e)
		}
	}
	if _, err = s.ReadDocument(ctx, recall.Scope{UserID: "other", AgentID: scope.AgentID}, 0, note.Ref); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("scope leak", err)
	}
	if _, err = s.ReadDocument(ctx, scope, at.Add(time.Second).UnixMicro(), note.Ref); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("privacy floor ignored", err)
	}
	_, err = write("replace", "n2", memory.DocumentWrite{Ref: note.Ref, Revision: 1, Layer: memory.Notes, Title: "Corrected strategy", Content: "The previous timing hypothesis was disproved."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write("stale", "n3", memory.DocumentWrite{Ref: note.Ref, Revision: 1, Layer: memory.Notes, Title: "stale", Content: "stale"}); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("stale revision overwrote correction", err)
	}
	if stale, e := s.StaleInjected(ctx, scope, 0); e != nil || !stale {
		t.Fatal("invalidated injected context was not scheduled for refresh", stale, e)
	}
	for _, ref := range []string{note.Ref, wiki.Ref, bootstrap.Ref} {
		if _, err = s.ReadDocument(ctx, scope, 0, ref); !errors.Is(err, recall.ErrConflict) {
			t.Fatal("stale dependency remained readable", ref, err)
		}
	}
	hits, err := s.Search(ctx, scope, recall.Query{Query: "navigation"})
	if err != nil || len(hits) != 0 {
		t.Fatal("stale dependency recalled", hits, err)
	}
	for _, name := range []string{"memory.db", "memory.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(filepath.Dir(s.pathForTest()), name))
		for _, secret := range []string{"Private strategy title", "timing hypothesis", "Navigation wiki"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("plaintext at rest", name, secret)
			}
		}
	}
}

func TestSemanticCandidatesContinueBeyondOneRequestCapacity(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	for i := 0; i < 130; i++ {
		content := "unrelated archived preference"
		if i == 0 {
			content = "rare invariant about navigation"
		}
		err := s.Apply(ctx, memory.Record{ID: fmt.Sprintf("fact-%03d", i), UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: "recall", Content: content, CreatedAt: time.Now(), SourceRefs: []memory.SourceRef{{Type: "conversation", Ref: fmt.Sprintf("conversation:%d", i)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	facts, err := s.SemanticCandidates(ctx, scope, 0, "rare invariant navigation")
	if err != nil || len(facts) != 1 || facts[0].Ref != "memory:fact-000" {
		t.Fatal("large corpus stopped or selected unrelated facts", facts, err)
	}
}

func TestLayeredMemoryRetirementDoesNotForgetOriginalEvidence(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	sources := []memory.SourceRef{{Type: "conversation", Ref: "conversation:one"}}
	d, err := s.WriteDocument(ctx, scope, 0, "create", "one", time.Now(), sources, memory.DocumentWrite{Layer: memory.Notes, Title: "Plan", Content: "A plan that can change."})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WriteDocument(ctx, scope, 0, "retire", "unused", time.Now(), sources, memory.DocumentWrite{Ref: d.Ref, Layer: d.Layer, Revision: d.Revision, Retire: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadDocument(ctx, scope, 0, d.Ref); !errors.Is(err, recall.ErrConflict) {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM memory_suppression`).Scan(&count)
	if count != 0 {
		t.Fatal("tidying created privacy suppression")
	}
}

func TestLayeredMemoryProofQuotesPersistSealedWithScopedSources(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	at := time.Now()
	source := memory.SourceRef{Type: "conversation", Ref: "conversation:workshop"}
	proof := recall.Evidence{SourceRef: source.Ref, Author: "user", At: at, Quote: "PRIVATE_EXACT_QUOTE: workshop in Oslo"}
	w := memory.DocumentWrite{Layer: memory.Notes, Title: "Workshop", Content: "Reported workshop in Oslo.", Evidence: []recall.Evidence{proof}}
	doc, err := s.WriteDocument(ctx, scope, 0, "grounded-event", "grounded-id", at, []memory.SourceRef{source}, w)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadDocument(ctx, scope, 0, doc.Ref)
	if err != nil || len(got.Sources) != 1 || len(got.Evidence) != 1 || got.Evidence[0].Quote != proof.Quote || !got.Evidence[0].At.Equal(proof.At) {
		t.Fatal(got, err)
	}
	s.ConfigureLocal()
	hits, err := s.Search(ctx, scope, recall.Query{Query: "workshop"})
	if err != nil || len(hits) != 1 || len(hits[0].Evidence) != 1 || hits[0].Evidence[0].Quote != proof.Quote {
		t.Fatal(hits, err)
	}
	for _, name := range []string{"memory.db", "memory.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(filepath.Dir(s.pathForTest()), name))
		if strings.Contains(string(raw), "PRIVATE_EXACT_QUOTE") {
			t.Fatal("proof leaked to disk")
		}
	}
	w.Evidence[0].Quote = "different proof"
	if _, err := s.WriteDocument(ctx, scope, 0, "grounded-event", "other-id", at, []memory.SourceRef{source}, w); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("proof not covered by durable receipt", err)
	}
	if err := s.Forget(ctx, scope, "grounded-id", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadDocument(ctx, scope, 0, doc.Ref); !errors.Is(err, recall.ErrConflict) {
		t.Fatal("forgotten proof readable", err)
	}
}
