package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func installRecallPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	facts, _ := json.Marshal(contract.Facts{Query: "evidence", Candidates: []contract.Candidate{}})
	policy := rsi.Policy{ID: "recall-owner-fixture", AllowedContracts: []string{contract.Contract, contract.QueryContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "recall", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: facts}}, Expected: json.RawMessage(`{"entries":[]}`)},
		{InstanceID: "recall", Contract: contract.QueryContract, Input: json.RawMessage(`{"input_id":"facts"}`), Inputs: []rsi.InputData{{ID: "facts", Data: []byte(`{"latest":"evidence","previous":"","answer":""}`)}}, Expected: json.RawMessage(`{"query":"evidence"}`)},
	}}
	return installRuntimeFeature(t, s, r, "recall", policy)
}
func recallTestVault(t *testing.T, r *Runs) {
	t.Helper()
	secrets := secretmemory.New()
	if err := secrets.Put(context.Background(), "credential:recall-test", []byte(strings.Repeat("r", 32))); err != nil {
		t.Fatal(err)
	}
	r.vault = memory.Vault{Secrets: secrets, Ref: "credential:recall-test"}
}
func TestRecallPluginFrozenPerTurnAndFenced(t *testing.T) { testRecallFrozenPerTurn(t, true) }

func TestRecallPluginCandidateSnapshotSurvivesCorpusDriftAndForget(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, _ := loopFixture(t)
	installRecallPlugin(t, s, r)
	recallTestVault(t, r)
	registry, err := tool.NewRegistry([]tool.Definition{recall.Definition("recall:snapshot-fixture")})
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	p := &explicitRecallProbe{hits: []recall.Hit{{SourceType: "memory", SourceRef: "memory:original", Content: "launch ORIGINAL_PRIVATE_EVIDENCE", OccurredAt: time.Now().Add(-time.Hour), Relevance: 1}}}
	r.ConfigureRecall(recall.Service{Memory: p, Conversation: recall.EmptyMemory{}})
	receipt := mustSubmit(t, a, "recall-candidate-recovery", "launch evidence")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.TextMessage("user", "launch evidence"))
	if err = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	interrupted, cancel := context.WithCancel(ctx)
	s.MainBoundary = func(name string) {
		if name == "after_recall_candidates_commit" {
			cancel()
		}
	}
	if _, err = r.Generation(interrupted, c, run); err == nil {
		t.Fatal("source snapshot interruption not exercised")
	}
	s.MainBoundary = nil
	cancel()
	if len(p.queries) != 1 {
		t.Fatal("source lookup count", len(p.queries))
	}
	p.hits = []recall.Hit{{SourceType: "memory", SourceRef: "memory:late", Content: "launch LATE_CORPUS_CHANGE", OccurredAt: time.Now().Add(-time.Minute), Relevance: 1}}
	gen, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(gen.Request.Messages)
	if len(p.queries) != 1 || !strings.Contains(string(raw), "ORIGINAL_PRIVATE_EVIDENCE") || strings.Contains(string(raw), "LATE_CORPUS_CHANGE") {
		t.Fatal("recovery reread changing provider", len(p.queries), string(raw))
	}
	var payload string
	var sealed bool
	if err = s.db.QueryRow(`SELECT payload,sealed FROM recall_candidate_snapshot WHERE run_id=?`, run.ID).Scan(&payload, &sealed); err != nil || !sealed || strings.Contains(payload, "ORIGINAL_PRIVATE_EVIDENCE") || !strings.HasPrefix(payload, "sealed:v1:") {
		t.Fatal("memory candidates not sealed", err)
	}
	if _, err = s.db.Exec(`UPDATE recall_candidate_snapshot SET payload='changed' WHERE run_id=?`, run.ID); err == nil {
		t.Fatal("mutable source snapshot")
	}
	r.plugins = nil // Forget clears retained bodies even if this embedder no longer mounts plugins.
	if err = r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, run.AgentID) }); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM recall_candidate_snapshot WHERE run_id=? AND payload IS NOT NULL`, run.ID) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND result_json IS NOT NULL`, run.ID) != 0 {
		t.Fatal("Forget retained derived content")
	}
	if _, err = r.searchRecall(ctx, c, "recall-auto:0", "", recall.Scope{UserID: "local", AgentID: run.AgentID}, recall.Query{Query: "launch"}, 4096); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("privacy revocation bypassed", err)
	}
}

func TestRecallPluginAcceptedNativeSearchPreservesOriginalReadWindow(t *testing.T) {
	s, r, _, a, p := loopFixture(t)
	installRecallPlugin(t, s, r)
	registry, err := tool.NewRegistry([]tool.Definition{recall.Definition(recall.UnconfiguredIdentity)})
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	text := strings.Repeat("background ", 1500) + "workshop was held on April 18. " + strings.Repeat("tail ", 300)
	sources := &explicitRecallProbe{hits: []recall.Hit{{SourceType: "conversation", SourceRef: "conversation:original", Content: text, OccurredAt: time.Now().Add(-time.Hour)}}}
	r.ConfigureRecall(recall.Service{Memory: recall.EmptyMemory{}, Conversation: sources})
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "completed bounded search"}, nil
		}
		return calls(recall.ToolName, `{"query":"workshop","limit":5}`), nil
	}
	receipt := mustSubmit(t, a, "recall-native-search", "find the original workshop evidence")
	mustDrain(t, a)
	var raw string
	if err = s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, receipt.RunID, recall.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil {
		t.Fatal(raw)
	}
	var hits []recall.Hit
	if json.Unmarshal([]byte(result.Text), &hits) != nil || len(hits) != 1 {
		t.Fatal(result.Text)
	}
	h := hits[0]
	if h.SourceRef != "conversation:original" || h.Offset == 0 || h.TotalBytes != len(text) || h.Content != text[h.Offset:h.Offset+len(h.Content)] || !strings.Contains(h.Content, "April 18") || len(result.Text) > 8192 {
		t.Fatal("plugin altered source/window", h)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='recall' AND result_json IS NOT NULL`, receipt.RunID) != 1 {
		t.Fatal("native search bypassed independent policy")
	}
	before := len(sources.queries)
	mustDrain(t, a)
	if len(sources.queries) != before {
		t.Fatal("completed search repeated provider")
	}
}
