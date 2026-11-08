package sqlite

import (
	"context"
	"encoding/json"
	secrets "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func semanticFixture(t *testing.T) (*Store, recall.Scope) {
	t.Helper()
	s, e := Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	key := secrets.New()
	key.Put(context.Background(), "credential:test", []byte(strings.Repeat("k", 32)))
	if e = s.ConfigureVault(memory.Vault{Secrets: key, Ref: "credential:test"}); e != nil {
		t.Fatal(e)
	}
	s.ConfigureLocal()
	return s, recall.Scope{UserID: "local", AgentID: "owner"}
}
func TestSemanticMergeEvidenceCorrectionReplayAndWithdrawal(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	quotes := []string{"我喜欢无糖绿茶", "I prefer green tea without sugar", "我更喜欢不加糖的绿茶"}
	var last memory.CurationInput
	var proposal memory.Proposal
	for i, quote := range quotes {
		facts, e := s.SemanticContext(ctx, scope, 0)
		if e != nil {
			t.Fatal(e)
		}
		op, target := "add", ""
		if len(facts) > 0 {
			op, target = "support", facts[0].Ref
		}
		c := memory.Candidate{Operation: op, Target: target, Facet: "drinks.preference", Category: "preferences", Quote: quote}
		if op == "add" {
			c.Content = "用户偏好无糖绿茶"
		}
		in := memory.CurationInput{Observation: memory.Observation{EventID: quote, Source: memory.SourceRef{Type: "conversation", Ref: "conversation:" + string(rune('a'+i))}, Author: "user", Text: quote, At: base.Add(time.Duration(i) * time.Minute)}, Existing: facts}
		p := memory.Proposal{Facts: []memory.Candidate{c}}
		if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
			t.Fatal(e)
		}
		last, proposal = in, p
	}
	if e := s.MergeSemantic(ctx, scope, last, proposal); e != nil {
		t.Fatal("cross-DB retry changed proposal", e)
	}
	hits, e := s.Search(ctx, scope, recall.Query{Query: "绿茶"})
	if e != nil || len(hits) != 1 || len(hits[0].Sources) != 3 {
		t.Fatal(hits, e)
	}
	var tier string
	s.db.QueryRow(`SELECT tier FROM memory_record WHERE memory_id=?`, strings.TrimPrefix(hits[0].SourceRef, "memory:")).Scan(&tier)
	if tier != "core" {
		t.Fatal("independent source support did not promote", tier)
	}
	facts, _ := s.SemanticContext(ctx, scope, 0)
	old := facts[0].Ref
	in := memory.CurationInput{Observation: memory.Observation{EventID: "correction", Source: memory.SourceRef{Type: "conversation", Ref: "conversation:correction"}, Author: "user", Text: "我现在更喜欢红茶", At: base.Add(5 * time.Minute)}, Existing: facts}
	p := memory.Proposal{Facts: []memory.Candidate{{Operation: "replace", Target: old, Facet: "drinks.preference", Category: "preferences", Quote: in.Observation.Text, Content: "用户偏好红茶"}}}
	if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	facts, _ = s.SemanticContext(ctx, scope, 0)
	if len(facts) != 1 || facts[0].Content != "用户偏好红茶" {
		t.Fatal(facts)
	}
	in = memory.CurationInput{Observation: memory.Observation{EventID: "withdraw", Source: memory.SourceRef{Type: "conversation", Ref: "conversation:withdraw"}, Author: "user", Text: "我不再偏好红茶", At: base.Add(6 * time.Minute)}, Existing: facts}
	p = memory.Proposal{Facts: []memory.Candidate{{Operation: "retract", Target: facts[0].Ref, Facet: "drinks.preference", Category: "preferences", Quote: in.Observation.Text}}}
	if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	hits, e = s.Search(ctx, scope, recall.Query{Query: "红茶"})
	if e != nil || len(hits) != 0 {
		t.Fatal("withdrawn fact recalled", hits, e)
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(s.pathForTest()), "memory.db"))
	if strings.Contains(string(raw), "用户偏好") {
		t.Fatal("semantic plaintext persisted")
	}
}
func (s *Store) pathForTest() string {
	var file string
	s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file)
	return file
}
func TestSemanticExpiryLegacyRetirementAndOptOut(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-181 * 24 * time.Hour)
	text := "我喜欢无糖绿茶"
	in := memory.CurationInput{Observation: memory.Observation{EventID: "old", Author: "user", Text: text, At: at, Source: memory.SourceRef{Type: "conversation", Ref: "conversation:old"}}, Existing: []memory.Fact{}}
	p := memory.Proposal{Facts: []memory.Candidate{{Operation: "add", Facet: "drinks.preference", Category: "preferences", Content: "用户偏好无糖绿茶", Quote: text}}}
	if e := s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	hits, e := s.Search(ctx, scope, recall.Query{Query: "绿茶"})
	if e != nil || len(hits) != 0 {
		t.Fatal("expired low confidence fact recalled", hits, e)
	}
	if e = s.Curate(ctx, "legacy", scope, memory.SourceRef{Type: "conversation", Ref: "conversation:legacy"}, time.Now(), []string{"给我调研 grokbot 赚钱"}, false, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.RetireAutomatic(ctx, []string{"legacy"}); e != nil {
		t.Fatal(e)
	}
	hits, e = s.Search(ctx, scope, recall.Query{Query: "grokbot"})
	if e != nil || len(hits) != 0 {
		t.Fatal("legacy extraction still used", hits, e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM memory_record WHERE memory_id='legacy-0'`).Scan(&n)
	if n != 1 {
		t.Fatal("original audit deleted")
	}
	if e = s.ForgetCategory(ctx, scope, "preferences", time.Now(), "optout"); e != nil {
		t.Fatal(e)
	}
	in.Observation.EventID = "blocked"
	in.Observation.At = time.Now()
	if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	facts, e := s.SemanticContext(ctx, scope, 0)
	if e != nil || len(facts) != 0 {
		raw, _ := json.Marshal(facts)
		t.Fatal(string(raw), e)
	}
}

func TestSemanticLifecycleDemotesCoreWithoutInventingEvidence(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	base := time.Now().Add(-170 * 24 * time.Hour)
	for i, quote := range []string{"我喜欢无糖绿茶", "I prefer green tea without sugar", "我更喜欢无糖绿茶"} {
		facts, e := s.SemanticContext(ctx, scope, 0)
		if e != nil {
			t.Fatal(e)
		}
		c := memory.Candidate{Operation: "add", Facet: "drinks.preference", Category: "preferences", Quote: quote, Content: "用户偏好无糖绿茶"}
		if len(facts) > 0 {
			c.Operation = "support"
			c.Target = facts[0].Ref
			c.Content = ""
		}
		in := memory.CurationInput{Observation: memory.Observation{EventID: quote, Author: "user", Text: quote, At: base.Add(time.Duration(i) * time.Minute), Source: memory.SourceRef{Type: "conversation", Ref: "conversation:" + string(rune('a'+i))}}, Existing: facts}
		if e = s.MergeSemantic(ctx, scope, in, memory.Proposal{Facts: []memory.Candidate{c}}); e != nil {
			t.Fatal(e)
		}
	}
	var core int
	if e := s.db.QueryRow(`SELECT count(*) FROM memory_record m WHERE tier='core' AND NOT EXISTS(SELECT 1 FROM memory_record n WHERE n.supersedes_memory_id=m.memory_id)`).Scan(&core); e != nil || core != 1 {
		t.Fatal("fixture did not establish Core", core, e)
	}
	if e := s.AgeSemantic(ctx, time.Now().Add(200*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := s.AgeSemantic(ctx, time.Now().Add(200*24*time.Hour)); e != nil {
		t.Fatal("lifecycle retry", e)
	}
	facts, e := s.SemanticContext(ctx, scope, 0)
	if e != nil || len(facts) != 1 {
		t.Fatal(facts, e)
	}
	var tier string
	s.db.QueryRow(`SELECT tier FROM memory_record WHERE memory_id=?`, strings.TrimPrefix(facts[0].Ref, "memory:")).Scan(&tier)
	if tier != "recall" {
		t.Fatal("stale Core not demoted", tier)
	}
	if e = s.AgeSemantic(ctx, time.Now().Add(200*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	// Query-time expiry is independently enforced at the persisted boundary.
	var expiry int64
	s.db.QueryRow(`SELECT expires_at FROM memory_semantic_record WHERE memory_id=?`, strings.TrimPrefix(facts[0].Ref, "memory:")).Scan(&expiry)
	if expiry != facts[0].At.Add(545*24*time.Hour).UnixMicro() {
		t.Fatal("decay fabricated recent confirmation", expiry)
	}
}
func TestSemanticForgetSuppressionExpiryRenewalAndInspection(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	text := "我有肾结石"
	in := memory.CurationInput{Observation: memory.Observation{EventID: "health", Author: "user", Text: text, At: time.Now().Add(-time.Hour), Source: memory.SourceRef{Type: "conversation", Ref: "conversation:health"}}, Existing: []memory.Fact{}}
	p := memory.Proposal{Facts: []memory.Candidate{{Operation: "add", Facet: "health.kidney_stones", Category: "health", Quote: text, Content: "用户报告患有肾结石"}}}
	if e := s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	if e := s.ForgetCategory(ctx, scope, "health", time.Now(), "forget-health"); e != nil {
		t.Fatal(e)
	}
	if e := s.AllowCategory(ctx, scope, "health", time.Now(), "allow-health"); e != nil {
		t.Fatal(e)
	}
	in.Observation.EventID = "replay-source"
	if e := s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	facts, e := s.SemanticContext(ctx, scope, 0)
	if e != nil || len(facts) != 0 {
		t.Fatal("forgotten source resurrected", facts, e)
	}
	in.Observation.EventID = "old-preference"
	in.Observation.Source.Ref = "conversation:old-preference"
	in.Observation.Text = "我喜欢绿茶"
	in.Observation.At = time.Now().Add(-181 * 24 * time.Hour)
	p.Facts[0] = memory.Candidate{Operation: "add", Facet: "drinks.preference", Category: "preferences", Quote: in.Observation.Text, Content: "用户偏好绿茶"}
	if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal(e)
	}
	page, _, e := s.Inspect(ctx, scope, 0, memory.InspectionQuery{Status: "retired"})
	if e != nil || len(page.Items) != 1 || page.Items[0].Content != "" {
		t.Fatal("expiry inspection wrong", page, e)
	}
	in.Observation.EventID = "new-preference"
	in.Observation.Source.Ref = "conversation:new-preference"
	in.Observation.At = time.Now()
	if e = s.MergeSemantic(ctx, scope, in, p); e != nil {
		t.Fatal("expired facet cannot renew", e)
	}
}

func TestSemanticMaintenanceKeepsAnInFlightIndexUsable(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	if e := s.Apply(ctx, memory.Record{ID: "existing", UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: "recall", Content: "用户偏好绿茶", CreatedAt: time.Now().Add(-time.Hour), SourceRefs: []memory.SourceRef{{Type: "feedback", Ref: "feedback:existing"}}}); e != nil {
		t.Fatal(e)
	}
	index, e := s.encryptedIndex(ctx, scope)
	if e != nil {
		t.Fatal(e)
	}
	in := memory.CurationInput{Observation: memory.Observation{EventID: "ordinary", Author: "user", Text: "hello", At: time.Now(), Source: memory.SourceRef{Type: "conversation", Ref: "conversation:ordinary"}}, Existing: []memory.Fact{}}
	if e = s.MergeSemantic(ctx, scope, in, memory.Proposal{Facts: []memory.Candidate{}}); e != nil {
		t.Fatal(e)
	}
	// Model admission may hold this working index while background observations
	// commit. Empty maintenance must not close that independent query's handle.
	var n int
	if e = index.QueryRowContext(ctx, `SELECT count(*) FROM recall_document`).Scan(&n); e != nil || n != 1 {
		t.Fatal("maintenance invalidated an unrelated search", n, e)
	}
}

func TestSemanticMigrationFromPopulatedV2PreservesOriginals(t *testing.T) {
	s, scope := semanticFixture(t)
	ctx := context.Background()
	record := memory.Record{ID: "explicit", UserID: scope.UserID, ScopeType: "agent", ScopeID: scope.AgentID, Tier: "core", Content: "用户喜欢绿茶", CreatedAt: time.Now(), SourceRefs: []memory.SourceRef{{Type: "feedback", Ref: "feedback:explicit"}}}
	if e := s.Apply(ctx, record); e != nil {
		t.Fatal(e)
	}
	var original string
	s.db.QueryRow(`SELECT content FROM memory_record WHERE memory_id='explicit'`).Scan(&original)
	if _, e := s.db.Exec(`DROP TABLE memory_document_dependency; DROP TABLE memory_document_receipt; DROP TABLE memory_document; DROP TABLE memory_semantic_record; DROP TABLE memory_retirement; DROP TABLE memory_semantic_event; PRAGMA user_version=2;`); e != nil {
		t.Fatal(e)
	}
	if e := s.migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var version int
	var after string
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	s.db.QueryRow(`SELECT content FROM memory_record WHERE memory_id='explicit'`).Scan(&after)
	if version != SchemaVersion || original != after {
		t.Fatal("migration rewrote existing memory", version)
	}
}
