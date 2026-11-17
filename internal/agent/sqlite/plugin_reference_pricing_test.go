package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installReferencePricing(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	raw, err := os.ReadFile("../../rsi/builtin/reference-pricing-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []rsi.Case
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return installRuntimeFeature(t, s, r, "reference-pricing", rsi.Policy{ID: "reference-pricing-owner-fixture", AllowedContracts: []string{contract.Service}, Cases: cases})
}
func TestReferencePricingActualRatesAndDeactivationNeverRestoreNative(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := loopFixture(t)
	input := modelInput()
	input.Endpoint, input.ModelID = "https://api.openai.com/v1", "gpt-6-luna"
	activateLoop(t, s, input)
	s.ConfigureLegacyReferenceEstimator(referenceEstimator{9000000})
	h := installReferencePricing(t, s, r)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}, nil
	}
	mustSubmit(t, a, "managed-rate", "hello")
	mustDrain(t, a)
	if scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate`) != 950000 || scalar(t, s, `SELECT count(*) FROM generation_reference_rates WHERE outcome='known'`) != 1 {
		t.Fatal("actual managed quote or long tier missing")
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "reference-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "unavailable-rate", "continue without a reference estimate")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM generation_cost_estimate`) != 1 || scalar(t, s, `SELECT count(*) FROM generation_reference_rates WHERE outcome='unavailable'`) != 1 {
		t.Fatal("disabled price producer restored native or blocked ordinary work")
	}
	if scalar(t, s, `SELECT count(*) FROM spend_reservation`) != 0 || scalar(t, s, `SELECT count(*) FROM spend_approval`) != 0 {
		t.Fatal("reference quote changed spending authority")
	}
	for _, query := range []string{`UPDATE generation_reference_rates SET rates_json='null'`, `DELETE FROM generation_reference_rates`, `UPDATE model_generation SET reference_rates_managed=0 WHERE reference_rates_managed=1`} {
		if _, err = s.db.Exec(query); err == nil {
			t.Fatal("mutable admitted rates", query)
		}
	}
}
func TestReferencePricingPendingColdUsesOriginalRatesWithoutProducer(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := loopFixture(t)
	input := modelInput()
	input.Endpoint, input.ModelID = "https://api.openai.com/v1", "gpt-6-luna"
	activateLoop(t, s, input)
	h := installReferencePricing(t, s, r)
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}, nil
	}
	receipt := mustSubmit(t, a, "frozen-reference", "hello")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	inputs, err := r.Steering(ctx, c, run.Checkpoint.InputSequence)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, input.Context...)
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{MessageID: input.Message.MessageID, Type: "user", Content: input.Message.Content})
		run.Checkpoint.InputSequence = input.Sequence
	}
	if err = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = r.Load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := r.Generation(ctx, c, run)
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM generation_reference_rates WHERE generation_id=? AND outcome='known'`, gen.Request.GenerationID) != 1 {
		t.Fatal("dispatch lacks admitted rates")
	}
	if err = r.Release(ctx, c); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "cold-reference-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	s, r, a = reopenMethodRuntime(t, s, r, a, p)
	s.ConfigureLegacyReferenceEstimator(referenceEstimator{9000000})
	r.plugins = nil
	// First dependent action after real reopen: no select, reload or policy repair.
	mustDrain(t, a)
	if scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate WHERE generation_id=?`, gen.Request.GenerationID) != 950000 || len(p.requests) != 1 || scalar(t, s, `SELECT count(*) FROM generation_reference_rates`) != 1 {
		t.Fatal("cold completion re-evaluated rates or repeated generation", len(p.requests))
	}
}
func TestReferencePricingRatesAndGenerationAcceptAtomically(t *testing.T) {
	s, r, _, a, p := loopFixture(t)
	installReferencePricing(t, s, r)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_reference_rates BEFORE INSERT ON generation_reference_rates BEGIN SELECT RAISE(ABORT,'reference receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, a, "reference-receipt-fault", "hello")
	if err := a.Drain(context.Background()); err == nil {
		t.Fatal("receipt failure admitted generation")
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation`) != 0 || scalar(t, s, `SELECT count(*) FROM generation_reference_rates`) != 0 || len(p.requests) != 0 {
		t.Fatal("generation escaped atomic price acceptance")
	}
}

func referenceVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/referencepricing/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
	code = strings.Replace(code, `catalog.Lookup(req)`, `independentReferenceLookup(catalog,req)`, 1)
	code += `
func independentReferenceLookup(c *Catalog,req contract.Request)*contract.Table{ t:=c.Lookup(req);if t!=nil {t.Base.Input*=2;for i:=range t.Tiers{t.Tiers[i].Rates.Input*=2};t.Version+="-independent11"};return t}
`
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/reference-pricing").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/reference-pricing/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}
func TestReferencePricingIndependentUpgradeRollbackRetainedRunAndCold(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, p := loopFixture(t)
	input := modelInput()
	input.Endpoint, input.ModelID = "https://api.openai.com/v1", "gpt-6-luna"
	activateLoop(t, s, input)
	s.ConfigureLegacyReferenceEstimator(referenceEstimator{9000000})

	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}, nil
	}
	legacy := mustSubmit(t, a, "rates-before-first-activation", "original pre-service request")
	h := installReferencePricing(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate c JOIN model_generation g USING(generation_id) WHERE g.run_id=?`, legacy.RunID) != 11000000 || scalar(t, s, `SELECT count(*) FROM generation_reference_rates`) != 0 {
		t.Fatal("pre-service Run changed its original estimator")
	}

	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := mustSubmit(t, a, "rates-old", "old request")
	pkg, err := h.Install(ctx, referenceVersionPackage(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next := graph.Graph
	next.Bindings = append([]rsiplugin.Binding(nil), next.Bindings...)
	next.Bindings[0].PackageDigest = pkg.Digest
	second, err := h.Prepare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "reference-pricing-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "reference-owner-double-input"
	for i := range policy.Cases {
		var expected contract.Response
		if err = json.Unmarshal(policy.Cases[i].Expected, &expected); err != nil {
			t.Fatal(err)
		}
		if expected.Table != nil {
			expected.Table.Base.Input *= 2
			for j := range expected.Table.Tiers {
				expected.Table.Tiers[j].Rates.Input *= 2
			}
			expected.Table.Version += "-independent11"
		}
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target, policy string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independently reviewed reference-rate change"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "answer", Usage: &llm.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}, nil
	}
	check := func(run string, amount int, revision string) {
		t.Helper()
		if scalar(t, s, `SELECT estimated_micros FROM generation_cost_estimate c JOIN model_generation g USING(generation_id) WHERE g.run_id=?`, run) != amount {
			t.Fatal("wrong frozen rate", run, amount)
		}
		var got string
		if err := s.db.QueryRow(`SELECT graph_revision FROM generation_reference_rates p JOIN model_generation g USING(generation_id) WHERE g.run_id=?`, run).Scan(&got); err != nil || got != revision {
			t.Fatal("rate provenance lost", got, revision, err)
		}
	}
	selectVersion("rate-upgrade", first.Revision, second.Revision, policy.ID)
	mustDrain(t, a)
	check(old.RunID, 950000, first.Revision)
	current := mustSubmit(t, a, "rates-new", "new request")
	mustDrain(t, a)
	check(current.RunID, 1150000, second.Revision)
	retained := mustSubmit(t, a, "rates-retained", "retained request")
	selectVersion("rate-rollback", second.Revision, first.Revision, "reference-pricing-owner-fixture")
	if err = h.Store.Retire(ctx, pkg.Digest); err != nil {
		t.Fatal(err)
	}
	s, _, a = reopenMethodRuntime(t, s, r, a, p)
	s.ConfigureLegacyReferenceEstimator(referenceEstimator{9000000})
	mustDrain(t, a)
	check(retained.RunID, 1150000, second.Revision)
	rolled := mustSubmit(t, a, "rates-rolled", "rolled back request")
	mustDrain(t, a)
	check(rolled.RunID, 950000, first.Revision)
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 || scalar(t, s, `SELECT count(*) FROM spend_approval`) != 0 || scalar(t, s, `SELECT count(*) FROM spend_reservation`) != 0 {
		t.Fatal("observational pricing created effects or spending grants")
	}
}
