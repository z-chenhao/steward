package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/pricing"
	"github.com/z-chenhao/assistant/internal/observability"
)

type referenceEstimator struct{ input int64 }

func (e referenceEstimator) Quote(s llm.Snapshot, _ llm.TokenUsage) (agent.ReferencePrice, bool) {
	return agent.ReferencePrice{Currency: "USD", Source: "test-catalog", Price: agent.ModelPrice{Model: s.ModelID, Version: "test-v1", Input: e.input, Output: 2000000, CacheRead: 100000, CacheWrite: 1500000}}, true
}

func TestAcceptedReferenceEstimateIsImmutableAndReadOnly(t *testing.T) {
	s, _, _, a, p := loopFixture(t)
	s.ConfigureCostEstimator(referenceEstimator{1000000})
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 3, OutputTokens: 4, CacheReadTokens: 5, CacheWriteTokens: 7}}, nil
	}
	receipt := mustSubmit(t, a, "estimated", "hello")
	mustDrain(t, a)
	if n := scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate`); n != 22 {
		t.Fatal("accepted usage estimate", n)
	}
	for _, table := range []string{"spend_reservation", "spend_approval"} {
		if scalar(t, s, `SELECT count(*) FROM `+table) != 0 {
			t.Fatal("estimate changed authorization", table)
		}
	}
	tree, err := s.Inspector().Observations(context.Background(), observability.ObservationQuery{RunID: receipt.RunID})
	if err != nil || len(tree.Trace.Metrics.Costs) != 1 || tree.Trace.Metrics.Unpriced != 0 {
		t.Fatal(tree, err)
	}
	cost := tree.Trace.Metrics.Costs[0]
	if cost.Micros != 22 || !cost.Estimated || cost.Source != "test-catalog · test-v1" {
		t.Fatal(cost)
	}
	// A new catalog and replay cannot revise a previously accepted estimate.
	s.ConfigureCostEstimator(referenceEstimator{9000000})
	mustSubmit(t, a, "estimated", "hello")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM generation_cost_estimate`) != 1 || scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate`) != 22 {
		t.Fatal("replay repriced history")
	}
	for _, query := range []string{
		`UPDATE generation_cost_estimate SET estimated_micros=0`,
		`DELETE FROM generation_cost_estimate`,
		`INSERT OR REPLACE INTO generation_cost_estimate SELECT * FROM generation_cost_estimate`,
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("mutable estimate", query)
		}
	}
}

func TestReferenceEstimateMissingUsageAndExplicitOverride(t *testing.T) {
	t.Run("failed response with known usage", func(t *testing.T) {
		s, _, _, a, p := loopFixture(t)
		s.ConfigureCostEstimator(referenceEstimator{1000000})
		p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
			return llm.GenerationResult{Usage: &llm.TokenUsage{InputTokens: 3}}, llm.ModelNotFound
		}
		mustSubmit(t, a, "failed", "hello")
		if err := a.Drain(context.Background()); !errors.Is(err, llm.ModelNotFound) {
			t.Fatal("expected provider failure", err)
		}
		if scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate`) != 3 {
			t.Fatal("lost known usage on failure")
		}
	})
	t.Run("missing usage", func(t *testing.T) {
		s, _, _, a, p := loopFixture(t)
		s.ConfigureCostEstimator(referenceEstimator{1000000})
		p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
			return llm.GenerationResult{Text: "answer"}, nil
		}
		mustSubmit(t, a, "missing", "hello")
		mustDrain(t, a)
		if scalar(t, s, `SELECT count(*) FROM generation_cost_estimate`) != 0 {
			t.Fatal("missing usage became zero cost")
		}
	})
	t.Run("manual override", func(t *testing.T) {
		s, r, a, p, c, run := spendRoot(t, 0)
		s.ConfigureCostEstimator(referenceEstimator{9000000})
		p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
			return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 3}}, nil
		}
		if err := executeTaskRun(t, a, r, context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if scalar(t, s, `SELECT actual_micros FROM spend_reservation WHERE generation_id IN (SELECT generation_id FROM model_generation WHERE run_id='`+run.ID+`')`) != 3 || scalar(t, s, `SELECT count(*) FROM generation_cost_estimate`) != 0 {
			t.Fatal("catalog overrode configured price")
		}
	})
}

func TestReferenceCatalogPricesAcceptedUsageWithoutConfiguration(t *testing.T) {
	s, _, _, a, p := loopFixture(t)
	input := modelInput()
	input.Endpoint, input.ModelID = "https://api.openai.com/v1", "gpt-6-luna"
	activateLoop(t, s, input)
	s.ConfigureCostEstimator(pricing.New())
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}, nil
	}
	receipt := mustSubmit(t, a, "auto-catalog", "hello")
	mustDrain(t, a)
	tree, err := s.Inspector().Observations(context.Background(), observability.ObservationQuery{RunID: receipt.RunID})
	// A million input tokens selects the catalog's long-context tier.
	if err != nil || len(tree.Trace.Metrics.Costs) != 1 {
		t.Fatal(tree, err)
	}
	cost := tree.Trace.Metrics.Costs[0]
	if cost.Micros != 950000 || !cost.Estimated || cost.Currency != "USD" || tree.Trace.Metrics.Unpriced != 0 {
		t.Fatal("automatic catalog quote not accepted", tree.Trace.Metrics)
	}
	if scalar(t, s, `SELECT count(*) FROM spend_reservation`) != 0 || scalar(t, s, `SELECT count(*) FROM spend_approval`) != 0 {
		t.Fatal("reference estimate entered monetary consent ledger")
	}
}

func TestEstimateMigrationDoesNotRepriceHistoricalGenerations(t *testing.T) {
	s, _, _, a, p := loopFixture(t)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 3}}, nil
	}
	receipt := mustSubmit(t, a, "historical", "hello")
	mustDrain(t, a)
	// Reconstruct a populated schema 46. The new table is empty; accepted
	// generation, snapshot and usage records remain untouched.
	if _, err := s.db.Exec(removeLayeredMemoryFixtureSchema + `DROP TABLE memory_cost_reservation; DROP TABLE memory_generation; DROP TABLE memory_semantic_receipt; DROP TABLE generation_cost_estimate; PRAGMA user_version=46`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	upgraded.ConfigureCostEstimator(referenceEstimator{9000000})
	tree, err := upgraded.Inspector().Observations(context.Background(), observability.ObservationQuery{RunID: receipt.RunID})
	if err != nil || tree.Trace.Metrics.Unpriced != 1 || len(tree.Trace.Metrics.Costs) != 0 || scalar(t, upgraded, `SELECT count(*) FROM generation_cost_estimate`) != 0 {
		t.Fatal("migration invented historical estimates", tree, err)
	}
}
