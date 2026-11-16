package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The second real process changes the owning template, not a Host proxy or
// metadata label. Its oracle is derived from the retained original Plan case.
func installPlanVersion116(t *testing.T, h rsi.Host, original string) (string, string) {
	t.Helper()
	ctx := context.Background()
	source := t.TempDir()
	if err := os.CopyFS(source, os.DirFS(h.Catalog.Path(original))); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	for _, name := range []string{"plugin.go", "plan_view.go"} {
		path, err := filepath.Abs("../../plugin/intelligentui/" + name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := string(raw)
		if name == "plugin.go" {
			code = strings.Replace(code, `Version: "1.27.0"`, `Version: "1.28.0"`, 1)
		} else {
			code = strings.Replace(code, "Plan: {{.Technology}}", "Plan v124: {{.Technology}}", 1)
		}
		if code == string(raw) {
			t.Fatal("variant implementation not changed", name)
		}
		replacement := filepath.Join(t.TempDir(), name)
		if err = os.WriteFile(replacement, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = replacement
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", overlayPath, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/intelligent-ui").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "steward.plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version, manifest.Files = "1.28.0", nil
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	pkg, err := h.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "intelligent-ui", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "intelligent-ui", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "ui-runtime-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "ui-plan-v124"
	changed := 0
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.PlanViews {
			var views contract.PlanViewsResult
			if err = json.Unmarshal(policy.Cases[i].Expected, &views); err != nil {
				t.Fatal(err)
			}
			for j := range views.Views {
				views.Views[j].HTML = strings.Replace(views.Views[j].HTML, "Plan: Go", "Plan v124: Go", 1)
			}
			policy.Cases[i].Expected, _ = json.Marshal(views)
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("missing independent Plan oracle")
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, pkg.Digest
}

func TestUIPlanViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base, err := h.ServiceInstance(ctx, first.Revision, contract.PlanViews)
	if err != nil {
		t.Fatal(err)
	}
	revision, digest := installPlanVersion116(t, h, base.PackageDigest)
	choose := func(id, from, to, policy string) {
		t.Helper()
		if e := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent frozen Plan template"}); e != nil {
			t.Fatal(e)
		}
		decision, e := h.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: from, Target: to, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Plan ready."}, nil
		}
		return calls(ui.ShowName, `{"title":"Plan","fallback":"Plan","components":[{"name":"plan_visualization","props":{"approach":"Ship core","technology":"Go","key_elements":["Loop","Host"]}}]}`), nil
	}
	var mu sync.Mutex
	previews := map[string][]string{}
	a.Sink = func(event agent.Event) {
		if event.UIPreview != nil && len(event.UIPreview.PlanViews) > 0 {
			mu.Lock()
			previews[event.RunID] = append(previews[event.RunID], event.UIPreview.PlanViews[0].HTML)
			mu.Unlock()
		}
	}
	type savedView struct{ ref, raw, graph string }
	saved := map[string]savedView{}
	assert := func(run, expectedPackage string, variant bool) {
		t.Helper()
		mu.Lock()
		pictures := append([]string(nil), previews[run]...)
		mu.Unlock()
		if len(pictures) == 0 {
			t.Fatal("real preview producer not observed", run)
		}
		for _, html := range pictures {
			if strings.Contains(html, "Plan v124:") != variant {
				t.Fatal("preview used current selection", run, html)
			}
		}
		var v savedView
		if e := s.db.QueryRow(`SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_plan_view_snapshot p USING(invocation_id) WHERE w.run_id=?`, run).Scan(&v.ref, &v.raw, &v.graph); e != nil {
			t.Fatal(e)
		}
		binding, e := h.ServiceInstance(ctx, v.graph, contract.PlanViews)
		if e != nil || binding.PackageDigest != expectedPackage {
			t.Fatal("view changed package", run, binding, e)
		}
		view, graph, e := s.WebPlanViewSource(ctx, v.ref+"#0")
		if e != nil || graph != v.graph || strings.Contains(view.HTML, "Plan v124:") != variant {
			t.Fatal("frozen view substituted", view, graph, e)
		}
		saved[run] = v
	}
	old := mustSubmit(t, a, "plan-old-pending", "Plan old")
	choose("plan-upgrade", first.Revision, revision, "ui-plan-v124")
	mustDrain(t, a)
	assert(old.RunID, base.PackageDigest, false)
	current := mustSubmit(t, a, "plan-new-current", "Plan new")
	mustDrain(t, a)
	assert(current.RunID, digest, true)
	pending := mustSubmit(t, a, "plan-new-pending", "Plan retained")
	choose("plan-rollback", revision, first.Revision, "ui-runtime-fixture")
	mustDrain(t, a)
	assert(pending.RunID, digest, true)
	rolled := mustSubmit(t, a, "plan-rolled", "Plan rolled")
	mustDrain(t, a)
	assert(rolled.RunID, base.PackageDigest, false)
	if _, err = h.Store.Deactivate(ctx, "plan-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	for run, v := range saved {
		if err = h.Store.CheckGraph(ctx, v.graph); err != nil {
			t.Fatal("retained graph unavailable", err)
		}
		view, graph, e := s.WebPlanViewSource(ctx, v.ref+"#0")
		if e != nil || view.HTML == "" || graph != v.graph {
			t.Fatal("deactivation lost original", run, e)
		}
		var raw string
		if e = s.db.QueryRow(`SELECT result_json FROM ui_plan_view_snapshot WHERE invocation_id=(SELECT invocation_id FROM ui_widget WHERE run_id=?)`, run).Scan(&raw); e != nil || raw != v.raw {
			t.Fatal("deactivation recomputed view", run, e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-plan%'`) != 0 {
		t.Fatal("pure producer left effects")
	}
}
