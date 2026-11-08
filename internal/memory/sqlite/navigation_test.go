package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

func TestLayeredMemoryNavigationDoesNotDecryptBodiesAndHonorsVisibility(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	doc, e := s.WriteDocument(ctx, scope, 0, "navigation", "navigation", time.Now(), []memory.SourceRef{{Type: "feedback", Ref: "feedback:nav"}}, memory.DocumentWrite{Layer: memory.Representations, Title: "Import procedure", Content: "Orchid UTF-8 without BOM " + strings.Repeat("large body ", 2000)})
	if e != nil {
		t.Fatal(e)
	}
	probe := &navigationSecrets{SecretStore: s.vault.Secrets}
	s.vault.Secrets = probe
	items, e := s.ReadNavigation(ctx, scope, 0, []string{doc.Ref, doc.Ref})
	if e != nil || len(items) != 1 || items[0].Title != "Import procedure" || !strings.Contains(strings.Join(items[0].Terms, " "), "orchid") {
		t.Fatal(items, e)
	}
	if probe.gets != 1 {
		t.Fatal("navigation decrypted more than metadata", probe.gets)
	}
	probe.gets = 0
	if _, e = s.ReadDocument(ctx, scope, 0, doc.Ref); e != nil || probe.gets != 2 {
		t.Fatal("full read must decrypt body and metadata", e, probe.gets)
	}
	for _, q := range []recall.Scope{{UserID: "foreign", AgentID: scope.AgentID}, {UserID: scope.UserID, AgentID: "foreign"}} {
		items, e = s.ReadNavigation(ctx, q, 0, []string{doc.Ref})
		if e != nil || len(items) != 0 {
			t.Fatal("scope leak", items, e)
		}
	}
	items, e = s.ReadNavigation(ctx, scope, time.Now().Add(time.Second).UnixMicro(), []string{doc.Ref})
	if e != nil || len(items) != 0 {
		t.Fatal("floor ignored", items, e)
	}
	if e = s.Forget(ctx, scope, "navigation", time.Now()); e != nil {
		t.Fatal(e)
	}
	items, e = s.ReadNavigation(ctx, scope, 0, []string{doc.Ref})
	if e != nil || len(items) != 0 {
		t.Fatal("forgotten navigation", items, e)
	}
}

type navigationSecrets struct {
	llm.SecretStore
	gets int
}

func (s *navigationSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	s.gets++
	return s.SecretStore.Get(ctx, ref)
}
