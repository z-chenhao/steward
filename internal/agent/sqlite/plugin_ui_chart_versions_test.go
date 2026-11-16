package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// A separately built package changes both chart layout policy and title.
// Existing lifecycle machinery must retain each Run’s original contribution.
func installChartVersion122(t *testing.T, h rsi.Host, original string) (string, string) {
	t.Helper()
	ctx := context.Background()
	source := t.TempDir()
	if err := os.CopyFS(source, os.DirFS(h.Catalog.Path(original))); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	for _, name := range []string{"plugin.go", "charts_view.go"} {
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
			worker, e := os.ReadFile("../../plugin/intelligentui/view/charts-worker.js")
			if e != nil {
				t.Fatal(e)
			}
			variant := strings.Replace(string(worker), "rows.length>6", "rows.length>4", 1)
			if variant == string(worker) {
				t.Fatal("layout implementation not changed")
			}
			code = strings.Replace(code, "//go:embed view/charts-worker.js\nvar chartsWorker string", "var chartsWorker = "+strconv.Quote(variant), 1)
			code = strings.Replace(code, "Title: props.Title", `Title: "Chart v124: " + props.Title`, 1)
		}
		if code == string(raw) {
			t.Fatal("variant implementation not changed", name)
		}
		replacement := filepath.Join(t.TempDir(), name+".txt")
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
	policy.ID = "ui-charts-v124"
	changed := 0
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.ChartViews {
			var views contract.ChartViewsResult
			if err = json.Unmarshal(policy.Cases[i].Expected, &views); err != nil {
				t.Fatal(err)
			}
			for j := range views.Views {
				views.Views[j].Title = "Chart v124: " + views.Views[j].Title
				views.Views[j].Worker = strings.Replace(views.Views[j].Worker, "rows.length>6", "rows.length>4", 1)
			}
			policy.Cases[i].Expected, _ = json.Marshal(views)
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("missing independent Charts oracle")
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, pkg.Digest
}

func TestUIChartViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base, err := h.ServiceInstance(ctx, first.Revision, contract.ChartViews)
	if err != nil {
		t.Fatal(err)
	}
	revision, digest := installChartVersion122(t, h, base.PackageDigest)
	choose := func(id, from, to, policy string) {
		t.Helper()
		if e := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent frozen chart layout"}); e != nil {
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
			return llm.GenerationResult{Text: "Charts ready."}, nil
		}
		return calls(ui.ShowName, `{"title":"Charts","fallback":"Trend","components":[{"name":"barChart","props":{"title":"Trend","description":"Original","data":[{"label":"A","value":1},{"label":"B","value":2},{"label":"C","value":3},{"label":"D","value":4},{"label":"E","value":5}]}}]}`), nil
	}
	var mu sync.Mutex
	previews := map[string][]string{}
	a.Sink = func(event agent.Event) {
		if event.UIPreview != nil && len(event.UIPreview.ChartViews) > 0 {
			mu.Lock()
			previews[event.RunID] = append(previews[event.RunID], event.UIPreview.ChartViews[0].Title)
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
			if strings.Contains(html, "Chart v124:") != variant {
				t.Fatal("preview used current selection", run, html)
			}
		}
		var v savedView
		if e := s.db.QueryRow(`SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_chart_view_snapshot p USING(invocation_id) WHERE w.run_id=?`, run).Scan(&v.ref, &v.raw, &v.graph); e != nil {
			t.Fatal(e)
		}
		binding, e := h.ServiceInstance(ctx, v.graph, contract.ChartViews)
		if e != nil || binding.PackageDigest != expectedPackage {
			t.Fatal("view changed package", run, binding, e)
		}
		view, _, graph, e := s.WebChartViewSource(ctx, v.ref+"#0")
		if e != nil || graph != v.graph || strings.Contains(view.Title, "Chart v124:") != variant {
			t.Fatal("frozen view substituted", view, graph, e)
		}
		if strings.Contains(view.Worker, "rows.length>4") != variant {
			t.Fatal("frozen layout policy substituted", run)
		}
		script := `const vm=require('node:vm');const fs=require('node:fs');const replies=[];const context={postMessage:value=>replies.push(value)};vm.createContext(context);vm.runInContext(fs.readFileSync(0,'utf8'),context);context.onmessage({data:{type:'init',seq:1,facts:{kind:'bar',data:['A','B','C','D','E'].map(label=>({label,value:1}))}}});process.stdout.write(JSON.stringify(replies[0].projection.horizontal));`
		command := exec.Command("node", "-e", script)
		command.Stdin = strings.NewReader(view.Worker)
		output, failure := command.CombinedOutput()
		if failure != nil || string(output) != strconv.FormatBool(variant) {
			t.Fatal("actual frozen layout behavior", string(output), failure)
		}
		saved[run] = v
	}
	old := mustSubmit(t, a, "charts-old-pending", "Charts old")
	choose("charts-upgrade", first.Revision, revision, "ui-charts-v124")
	mustDrain(t, a)
	assert(old.RunID, base.PackageDigest, false)
	current := mustSubmit(t, a, "charts-new-current", "Charts new")
	mustDrain(t, a)
	assert(current.RunID, digest, true)
	pending := mustSubmit(t, a, "charts-new-pending", "Charts retained")
	choose("charts-rollback", revision, first.Revision, "ui-runtime-fixture")
	mustDrain(t, a)
	assert(pending.RunID, digest, true)
	rolled := mustSubmit(t, a, "charts-rolled", "Charts rolled")
	mustDrain(t, a)
	assert(rolled.RunID, base.PackageDigest, false)
	if _, err = h.Store.Deactivate(ctx, "charts-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	for run, v := range saved {
		if err = h.Store.CheckGraph(ctx, v.graph); err != nil {
			t.Fatal("retained graph unavailable", err)
		}
		view, _, graph, e := s.WebChartViewSource(ctx, v.ref+"#0")
		if e != nil || view.Title == "" || graph != v.graph {
			t.Fatal("deactivation lost original", run, e)
		}
		var raw string
		if e = s.db.QueryRow(`SELECT result_json FROM ui_chart_view_snapshot WHERE invocation_id=(SELECT invocation_id FROM ui_widget WHERE run_id=?)`, run).Scan(&raw); e != nil || raw != v.raw {
			t.Fatal("deactivation recomputed view", run, e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-charts%'`) != 0 {
		t.Fatal("pure producer left effects")
	}
}

// Child inherits the original graph without gaining Main-only publication rights.
func TestUIChartChildAdmissionAfterUpgradeKeepsParentGraphAndService(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.ServiceInstance(ctx, first.Revision, contract.ChartViews)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := installChartVersion122(t, h, original.PackageDigest)
	c, run, gen, toolCalls := childAdmission(t, s, r, a, p, `{"task":"child explicit input"}`)
	released := false
	defer func() {
		if !released {
			_ = r.Release(ctx, c)
		}
	}()
	if err = h.Propose(ctx, rsi.Candidate{ID: "charts-child-upgrade", Baseline: first.Revision, Target: target, PolicyID: "ui-charts-v124", Goal: "Child inherits parent Chart graph"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "charts-child-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "charts-child-upgrade", Expected: first.Revision, Target: target, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	// Admit from the real frozen generation only after the active graph changed.
	if _, err = r.ToolStore(c).Accept(ctx, gen.Request.GenerationID, toolCalls, run.Tools); err != nil {
		t.Fatal(err)
	}
	if err = r.Release(ctx, c); err != nil {
		t.Fatal(err)
	}
	released = true
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 2 {
		t.Fatal("actual Child/grandchild execution missing")
	}
	if scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.parent_run_id IS NOT NULL AND cb.graph_revision=pb.graph_revision AND cb.graph_revision=? AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?)`, first.Revision, contract.ChartViews) != 2 {
		t.Fatal("Child/grandchild adopted active Chart graph or lost service fence")
	}
	var childGraph string
	if err = s.db.QueryRow(`SELECT b.graph_revision FROM rsi_run b JOIN run c USING(run_id) WHERE c.worker_position='child' ORDER BY c.rowid LIMIT 1`).Scan(&childGraph); err != nil {
		t.Fatal(err)
	}
	binding, err := h.ServiceInstance(ctx, childGraph, contract.ChartViews)
	if err != nil || binding.PackageDigest != original.PackageDigest {
		t.Fatal("Child resolved upgraded package", binding, err)
	}
	raw, err := h.ReadService(ctx, childGraph, contract.ChartViews, json.RawMessage(`{}`), []rsi.InputData{{ID: contract.ChartInput, Data: []byte(`{"charts":[{"component":0,"name":"barChart","props":{"title":"Child choices","description":"Original","data":[{"label":"A","value":1}]}}]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	var views contract.ChartViewsResult
	if rsiplugin.DecodeResult(raw, &views) != nil || len(views.Views) != 1 || strings.Contains(views.Views[0].Title, "Chart v124:") || !strings.Contains(views.Views[0].Title, "Child choices") {
		t.Fatal("Child frozen pure service substituted", string(raw))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_chart_view_snapshot`) != 0 {
		t.Fatal("worker acquired a Main UI publication")
	}
	active, err := h.Store.Status(ctx)
	if err != nil || active.Revision != target {
		t.Fatal("test repaired active selection", active, err)
	}
}
