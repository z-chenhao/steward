package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/recall"
)

type semanticProvider struct {
	r            *Runs
	calls        int
	reply        func(memory.CurationInput) llm.GenerationResult
	beforeAccept func()
	beforeReply  func(context.Context, llm.GenerationRequest)
}

func (p *semanticProvider) Generate(ctx context.Context, req llm.GenerationRequest, _ func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	p.calls++
	if len(req.Tools) != 0 || len(req.Messages) != 2 || (req.Messages[0].Text != memory.CurationInstruction && req.Messages[0].Text != memory.DreamInstruction && req.Messages[0].Text != memory.GroundedDreamInstruction && req.Messages[0].Text != memory.WorkerDreamInstruction) || !strings.HasPrefix(req.SessionID, "memory:") {
		return llm.GenerationResult{}, llm.InvalidProviderResponse
	}
	release, e := req.DispatchGuard(ctx)
	if e != nil {
		return llm.GenerationResult{}, e
	}
	release()
	if p.beforeReply != nil {
		p.beforeReply(ctx, req)
	}
	var in memory.CurationInput
	if json.Unmarshal([]byte(req.Messages[1].Text), &in) != nil {
		return llm.GenerationResult{}, llm.InvalidProviderResponse
	}
	result := p.reply(in)
	if p.beforeAccept != nil {
		p.beforeAccept()
	}
	e = p.r.host.Models().RecordUsageAnd(ctx, req, result, time.Millisecond, nil, func(ctx context.Context, tx *sql.Tx, r llm.GenerationRequest, v llm.GenerationResult, cause error) error {
		return p.r.AcceptMemoryGeneration(ctx, tx, r, v, cause)
	})
	return result, e
}
func (p *semanticProvider) Continue(ctx context.Context, r llm.GenerationRequest, s func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.Generate(ctx, r, s)
}
func (p *semanticProvider) Cancel(context.Context, string) error  { return nil }
func (p *semanticProvider) Dispose(context.Context, string) error { return nil }
func semanticRuntime(t *testing.T) (*Store, *Runs, *memorysqlite.Store, agent.Actor, *semanticProvider) {
	t.Helper()
	s, r, _, a, _ := loopFixture(t)
	ctx := context.Background()
	m, e := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	secrets := secretmemory.New()
	if e = secrets.Put(ctx, "credential:memory", []byte(strings.Repeat("k", 32))); e != nil {
		t.Fatal(e)
	}
	vault := memory.Vault{Secrets: secrets, Ref: "credential:memory"}
	if e = m.ConfigureVault(vault); e != nil {
		t.Fatal(e)
	}
	m.ConfigureLocal()
	r.ConfigureMemory(m, vault)
	p := &semanticProvider{r: r, reply: func(in memory.CurationInput) llm.GenerationResult {
		c := memory.Candidate{Operation: "add", Facet: "drinks.preference", Category: "preferences", Content: "用户偏好无糖绿茶", Quote: strings.TrimSpace(in.Observation.Text)}
		if len(in.Existing) > 0 {
			c.Operation = "support"
			c.Target = in.Existing[0].Ref
			c.Content = ""
		}
		raw, _ := json.Marshal(memory.Proposal{Facts: []memory.Candidate{c}})
		return llm.GenerationResult{Text: string(raw), Usage: &llm.TokenUsage{InputTokens: 100, OutputTokens: 30}}
	}}
	r.ConfigureSemanticMemory(p)
	return s, r, m, a, p
}
func TestSemanticDreamDeadlineRetainsAcceptanceLeaseHeadroom(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	seen := map[string]bool{}
	p.beforeReply = func(ctx context.Context, req llm.GenerationRequest) {
		var input memory.CurationInput
		if err := json.Unmarshal([]byte(req.Messages[1].Text), &input); err != nil {
			t.Fatal(err)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("maintenance call has no deadline")
		}
		remaining := time.Until(deadline)
		if input.Mode == "dream" {
			if remaining <= time.Minute || remaining > 3*time.Minute {
				t.Fatal("document synthesis has no bounded extended window", remaining)
			}
		} else if remaining > time.Minute {
			t.Fatal("ordinary extraction inherited the longer synthesis deadline", remaining)
		}
		var lease int64
		if err := s.db.QueryRow(`SELECT lease_until FROM memory_generation WHERE generation_id=?`, req.GenerationID).Scan(&lease); err != nil {
			t.Fatal(err)
		}
		if !time.UnixMicro(lease).After(deadline.Add(15 * time.Second)) {
			t.Fatal("generation could finish after its acceptance lease expired")
		}
		seen[curationPurpose(input)] = true
	}
	p.reply = func(in memory.CurationInput) llm.GenerationResult {
		if in.Mode != "dream" {
			return llm.GenerationResult{Text: `{"facts":[]}`}
		}
		return llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
	}
	ctx := context.Background()
	mustSubmit(t, a, "history", "我喜欢无糖绿茶")
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ManageMemory(ctx, "consolidate", memory.Mutation{Operation: "dream"}); err != nil {
		t.Fatal(err)
	}
	if err := r.CurateMemory(ctx); err != nil {
		t.Fatal(err)
	}
	if !seen["facts"] || !seen["dream"] || scalar(t, s, `SELECT count(*) FROM memory_generation WHERE status!='completed'`) != 0 {
		t.Fatal("both maintenance purposes must complete through normal acceptance", seen)
	}
}

func TestSemanticRuntimeAcceptedReplayAndNoUserTask(t *testing.T) {
	s, r, m, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "one", "我喜欢无糖绿茶")
	tasks, runs := scalar(t, s, `SELECT count(*) FROM task`), scalar(t, s, `SELECT count(*) FROM run`)
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 1 || scalar(t, s, `SELECT count(*) FROM memory_generation WHERE status='completed'`) != 1 || scalar(t, s, `SELECT count(*) FROM provider_usage`) != 1 {
		t.Fatal("private generation/usage not accepted", p.calls)
	}
	// Simulate a crash after memory.db commit, before the state receipt.
	if _, e := s.db.Exec(`DELETE FROM memory_semantic_receipt`); e != nil {
		t.Fatal(e)
	}
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 1 {
		t.Fatal("accepted model work repeated after cross-db retry", p.calls)
	}
	mustSubmit(t, a, "two", "I prefer green tea without sugar")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	var agentID string
	s.db.QueryRow(`SELECT agent_id FROM agent_config`).Scan(&agentID)
	facts, e := m.SemanticContext(ctx, recall.Scope{UserID: "local", AgentID: agentID}, 0)
	if e != nil || len(facts) != 1 || facts[0].Content != "用户偏好无糖绿茶" {
		t.Fatal(facts, e)
	}
	if scalar(t, s, `SELECT count(*) FROM task`) != tasks || scalar(t, s, `SELECT count(*) FROM run`) != runs || scalar(t, s, `SELECT count(*) FROM model_generation`) != 0 || scalar(t, s, `SELECT count(*) FROM delivery_request`) != 0 {
		t.Fatal("maintenance leaked into user tasks, traces or delivery")
	}
	for _, file := range []string{"state.db", "state.db-wal", "memory.db", "memory.db-wal"} {
		raw, e := os.ReadFile(filepath.Join(filepath.Dir(s.path), file))
		if e == nil && strings.Contains(string(raw), "用户偏好无糖绿茶") {
			t.Fatal("private result plaintext in", file)
		}
	}
}
func TestSemanticRuntimeInvalidQuarantinesWithoutBlockingLaterInput(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	ctx := context.Background()
	p.reply = func(memory.CurationInput) llm.GenerationResult {
		return llm.GenerationResult{Text: `{"facts":[{"operation":"add","facet":"fake","category":"health","content":"invented","quote":"not in source","tentative":false}]}`}
	}
	mustSubmit(t, a, "bad", "我喜欢无糖绿茶")
	mustSubmit(t, a, "ordinary", "给我调研 grokbot 赚钱路线")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		e := r.CurateMemory(ctx)
		if i < 2 && !errors.Is(e, memory.ErrProposal) {
			t.Fatal("malformed proposal did not retry", e)
		}
		if i == 2 && e != nil {
			t.Fatal(e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE failed_at IS NOT NULL`) != 1 || scalar(t, s, `SELECT count(*) FROM memory_semantic_receipt`) != 1 || p.calls != 3 {
		t.Fatal("invalid output poisoned queue")
	}
}
func TestSemanticRuntimeForgetBeforeAcceptanceAndDispatch(t *testing.T) {
	s, r, m, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "private", "我喜欢无糖绿茶")
	p.beforeAccept = func() {
		if _, e := r.ManageMemory(ctx, "forget", memory.Mutation{Operation: "forget", Category: "all"}); e != nil {
			t.Fatal(e)
		}
	}
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e == nil {
		t.Fatal("late acceptance after forget")
	}
	if scalar(t, s, `SELECT count(*) FROM memory_generation WHERE status='completed'`) != 0 {
		t.Fatal("revoked private output accepted")
	}
	p.beforeAccept = nil
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	var agentID string
	s.db.QueryRow(`SELECT agent_id FROM agent_config`).Scan(&agentID)
	facts, e := m.SemanticContext(ctx, recall.Scope{UserID: "local", AgentID: agentID}, 0)
	if e != nil || len(facts) != 0 {
		t.Fatal(facts, e)
	}
	var id, token string
	s.db.QueryRow(`SELECT generation_id,owner_token FROM memory_generation`).Scan(&id, &token)
	if _, e = r.memoryGuard(ctx, llm.GenerationRequest{GenerationID: id}, token); !errors.Is(e, recall.ErrConflict) {
		t.Fatal("revoked request dispatched", e)
	}
}

func TestSemanticRuntimeExpiredAttemptCannotAcceptAndContextIsFresh(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "one", "我喜欢无糖绿茶")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	var event, agentID, ref string
	var at int64
	if e := s.db.QueryRow(`SELECT event_id,agent_id,source_ref,accepted_at FROM memory_mutation WHERE source_type='conversation' LIMIT 1`).Scan(&event, &agentID, &ref, &at); e != nil {
		t.Fatal(e)
	}
	in := memory.CurationInput{Observation: memory.Observation{EventID: event, Author: "user", Text: "我喜欢无糖绿茶", At: time.UnixMicro(at), Source: memory.SourceRef{Type: "conversation", Ref: "conversation:" + ref}}, Existing: []memory.Fact{}}
	scope := recall.Scope{UserID: "local", AgentID: agentID}
	old, token, _, e := r.prepareCuration(ctx, scope, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE memory_generation SET lease_until=0 WHERE generation_id=?`, old.Request.GenerationID); e != nil {
		t.Fatal(e)
	}
	next, _, cached, e := r.prepareCuration(ctx, scope, in)
	if e != nil || cached != nil || next.Request.GenerationID == old.Request.GenerationID {
		t.Fatal("expired attempt reused", e)
	}
	if _, e = r.memoryGuard(ctx, old.Request, token); !errors.Is(e, recall.ErrConflict) {
		t.Fatal("old lease dispatched", e)
	}
	result := p.reply(in)
	e = r.write(ctx, func(tx *sql.Tx) error { return r.AcceptMemoryGeneration(ctx, tx, old.Request, result, nil) })
	if !errors.Is(e, recall.ErrConflict) {
		t.Fatal("late attempt accepted", e)
	}
}

func TestSemanticRuntimeRejectsOversizedContextBeforeDispatch(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "large", "我喜欢无糖绿茶")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	var id, owner, source string
	var at int64
	if e := s.db.QueryRow(`SELECT event_id,agent_id,source_ref,accepted_at FROM memory_mutation WHERE operation='observe' LIMIT 1`).Scan(&id, &owner, &source, &at); e != nil {
		t.Fatal(e)
	}
	in := memory.CurationInput{Observation: memory.Observation{EventID: id, Author: "user", Text: "我喜欢无糖绿茶", At: time.UnixMicro(at), Source: memory.SourceRef{Type: "conversation", Ref: "conversation:" + source}}, Existing: []memory.Fact{{Ref: "memory:large", Content: strings.Repeat("x", 1<<20)}}}
	if _, _, _, e := r.prepareCuration(ctx, recall.Scope{UserID: "local", AgentID: owner}, in); !errors.Is(e, llm.ContextLengthExceeded) {
		t.Fatal("oversized maintenance admitted", e)
	}
	if p.calls != 0 || scalar(t, s, `SELECT count(*) FROM memory_generation`) != 0 {
		t.Fatal("rejected input reached provider/journal")
	}
}

func TestSemanticMigrationFromV46PreservesAcceptedWork(t *testing.T) {
	s, _, _, a, _ := loopFixture(t)
	ctx := context.Background()
	receipt := mustSubmit(t, a, "accepted-before-upgrade", "original accepted request")
	original := scalar(t, s, `SELECT count(*) FROM conversation_intent`)
	if _, e := s.db.Exec(removeLayeredMemoryFixtureSchema + `DROP TABLE generation_cost_estimate; DROP TABLE memory_cost_reservation; DROP TABLE memory_generation; DROP TABLE memory_semantic_receipt; PRAGMA user_version=46;`); e != nil {
		t.Fatal(e)
	}
	if e := migrate(ctx, s.db, migrations); e != nil {
		t.Fatal(e)
	}
	if SchemaVersion() != 51 || scalar(t, s, `PRAGMA user_version`) != 51 || scalar(t, s, `SELECT count(*) FROM conversation_intent`) != original || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=?`, receipt.RunID) != 1 {
		t.Fatal("forward migration changed accepted work")
	}
	if scalar(t, s, `SELECT count(*) FROM memory_generation`) != 0 {
		t.Fatal("migration fabricated maintenance work")
	}
}

func TestSemanticRuntimeUnavailableDoesNotFallBackToLiteralMemory(t *testing.T) {
	s, r, m, a, _ := semanticRuntime(t)
	ctx := context.Background()
	r.ConfigureSemanticMemory(nil)
	mustSubmit(t, a, "no-model", "我喜欢无糖绿茶")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); !errors.Is(e, recall.ErrUnavailable) {
		t.Fatal("unavailable Curator did not defer", e)
	}
	var owner string
	if e := s.db.QueryRow(`SELECT agent_id FROM agent_config`).Scan(&owner); e != nil {
		t.Fatal(e)
	}
	facts, e := m.SemanticContext(ctx, recall.Scope{UserID: "local", AgentID: owner}, 0)
	if e != nil || len(facts) != 0 || scalar(t, s, `SELECT count(*) FROM memory_semantic_receipt`) != 0 || scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE applied_at IS NOT NULL`) != 0 {
		t.Fatal("unavailable model fabricated a literal fallback or successful receipt", e)
	}
}
