package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func recallPackageSource(t *testing.T) string { return recallPackageVersion(t, "1.0.1") }
func recallPackageVersion(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/recall")
	if version != "1.0.1" {
		path, err := filepath.Abs("../plugin/recallpolicy/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)

		if code == string(raw) {
			t.Fatal("trace overlay unchanged")
		}
		override := filepath.Join(t.TempDir(), "plugin.go")
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
		file := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(file, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/recall")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/recall/steward.plugin.json")
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

type recallSourceFixture struct{}

func (recallSourceFixture) Search(_ context.Context, scope recall.Scope, _ recall.Query) ([]recall.Hit, error) {
	return []recall.Hit{{SourceType: "conversation", SourceRef: "conversation:independent-original", Content: "project remembered evidence", OccurredAt: time.Now().Add(-time.Hour), Relevance: 1}}, nil
}
func TestRecallIndependentUpgradeRollbackAndLegacyRun(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t, recall.Definition("recall:independent-fixture"))
	f.runs.ConfigureRecall(recall.Service{Memory: recall.EmptyMemory{}, Conversation: recallSourceFixture{}})
	old, err := f.actor.Submit(ctx, "recall-before-activation", "project old input")
	if err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), Recall: recallPackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	first, _ := f.host.Store.Status(ctx)
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(runID string) int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='recall' AND result_json IS NOT NULL`, runID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(old.RunID) != 0 {
		t.Fatal("legacy run retroactively migrated")
	}
	current, err := f.actor.Submit(ctx, "recall-v1", "project new input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if count(current.RunID) != 2 {
		t.Fatal("actual automatic query and selection did not use binary", count(current.RunID))
	}
	p, err := f.host.Install(ctx, recallPackageVersion(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.host.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	next := g.Graph
	next.Bindings = append([]rsiplugin.Binding(nil), next.Bindings...)
	for i := range next.Bindings {
		if next.Bindings[i].InstanceID == "recall" {
			next.Bindings[i].PackageDigest = p.Digest
		}
	}
	second, err := f.host.Prepare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	var policy string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target string) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "independent recall policy evolution"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	selectVersion("recall-upgrade", first.Revision, second.Revision)
	if binding, err := f.host.Store.Binding(ctx, current.RunID); err != nil || binding != first.Revision {
		t.Fatal("in-flight version changed", binding, err)
	}
	upgraded, err := f.actor.Submit(ctx, "recall-v11", "project upgraded input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if binding, err := f.host.Store.Binding(ctx, upgraded.RunID); err != nil || binding != second.Revision || count(upgraded.RunID) != 2 {
		t.Fatal("new execution did not use independent upgrade", binding, err)
	}
	selectVersion("recall-rollback", second.Revision, first.Revision)
	if err = f.host.Store.Retire(ctx, p.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Catalog.Load(p.Digest); err != nil {
		t.Fatal("old implementation lost", err)
	}
	if _, err = f.host.Store.Deactivate(ctx, "recall-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.actor.Submit(ctx, "recall-disabled", "project disabled input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatal("disabled recall resurrected native", err)
	}
	var status string
	if err = f.db.QueryRow(`SELECT status FROM run WHERE run_id=?`, disabled.RunID).Scan(&status); err != nil || status != "failed" {
		t.Fatal(status, err)
	}
}
