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

// A separately built package changes the confirmation default and form title.
// Existing lifecycle machinery must retain each Run’s original contribution.
func installFormViewVersion124(t *testing.T, h rsi.Host, original string) (string, string) {
	t.Helper()
	ctx := context.Background()
	source := t.TempDir()
	if err := os.CopyFS(source, os.DirFS(h.Catalog.Path(original))); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	for _, name := range []string{"plugin.go", "form_view.go"} {
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
			code = strings.Replace(code, "Title: props.Title", `Title: "Form v124: " + props.Title`, 1)
			code = strings.Replace(code, `value = "false"`, `value = "true"`, 1)
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
	policy.ID = "ui-form-views-v124"
	changed := 0
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.FormViews {
			var views contract.FormViewsResult
			if err = json.Unmarshal(policy.Cases[i].Expected, &views); err != nil {
				t.Fatal(err)
			}
			for j := range views.Views {
				views.Views[j].Title = "Form v124: " + views.Views[j].Title
				for k := range views.Views[j].Fields {
					if views.Views[j].Fields[k].Type == "boolean" {
						views.Views[j].Fields[k].Default = "true"
					}
				}
			}
			policy.Cases[i].Expected, _ = json.Marshal(views)
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("missing independent Forms oracle")
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, pkg.Digest
}

func TestUIFormViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base, err := h.ServiceInstance(ctx, first.Revision, contract.FormViews)
	if err != nil {
		t.Fatal(err)
	}
	revision, digest := installFormViewVersion124(t, h, base.PackageDigest)
	choose := func(id, from, to, policy string) {
		t.Helper()
		if e := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent frozen form defaults"}); e != nil {
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
			return llm.GenerationResult{Text: "Forms ready."}, nil
		}
		return calls(ui.ShowName, `{"title":"Forms","fallback":"Original form","components":[{"name":"steward.form","props":{"title":"Original form","fields":[{"name":"notes","label":"Notes","type":"textarea"},{"name":"confirm","label":"Confirm","type":"boolean"}]}}]}`), nil
	}
	var mu sync.Mutex
	previews := map[string][]string{}
	a.Sink = func(event agent.Event) {
		if event.UIPreview != nil && len(event.UIPreview.FormViews) > 0 {
			mu.Lock()
			previews[event.RunID] = append(previews[event.RunID], event.UIPreview.FormViews[0].Props.Title+"|"+event.UIPreview.FormViews[0].Defaults["confirm"])
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
		expectedDefault := "false"
		if variant {
			expectedDefault = "true"
		}
		for _, html := range pictures {
			if !strings.HasSuffix(html, "|"+expectedDefault) {
				t.Fatal("preview confirmation default used active selection", run, html)
			}
			if strings.Contains(html, "Form v124:") != variant {
				t.Fatal("preview used current selection", run, html)
			}
		}
		var v savedView
		if e := s.db.QueryRow(`SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_form_view_snapshot p USING(invocation_id) WHERE w.run_id=?`, run).Scan(&v.ref, &v.raw, &v.graph); e != nil {
			t.Fatal(e)
		}
		binding, e := h.ServiceInstance(ctx, v.graph, contract.FormViews)
		if e != nil || binding.PackageDigest != expectedPackage {
			t.Fatal("view changed package", run, binding, e)
		}
		view, _, graph, e := s.WebFormViewSource(ctx, v.ref+"#0")
		if e != nil || graph != v.graph || strings.Contains(view.Title, "Form v124:") != variant {
			t.Fatal("frozen view substituted", view, graph, e)
		}
		if len(view.Fields) != 2 || (view.Fields[1].Default == "true") != variant {
			t.Fatal("frozen confirmation default substituted", run, view.Fields)
		}
		saved[run] = v
	}
	old := mustSubmit(t, a, "forms-old-pending", "Forms old")
	choose("forms-upgrade", first.Revision, revision, "ui-form-views-v124")
	mustDrain(t, a)
	assert(old.RunID, base.PackageDigest, false)
	current := mustSubmit(t, a, "forms-new-current", "Forms new")
	mustDrain(t, a)
	assert(current.RunID, digest, true)
	pending := mustSubmit(t, a, "forms-new-pending", "Forms retained")
	choose("forms-rollback", revision, first.Revision, "ui-runtime-fixture")
	mustDrain(t, a)
	assert(pending.RunID, digest, true)
	rolled := mustSubmit(t, a, "forms-rolled", "Forms rolled")
	mustDrain(t, a)
	assert(rolled.RunID, base.PackageDigest, false)
	if _, err = h.Store.Deactivate(ctx, "forms-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	for run, v := range saved {
		if err = h.Store.CheckGraph(ctx, v.graph); err != nil {
			t.Fatal("retained graph unavailable", err)
		}
		view, _, graph, e := s.WebFormViewSource(ctx, v.ref+"#0")
		if e != nil || view.Title == "" || graph != v.graph {
			t.Fatal("deactivation lost original", run, e)
		}
		var raw string
		if e = s.db.QueryRow(`SELECT result_json FROM ui_form_view_snapshot WHERE invocation_id=(SELECT invocation_id FROM ui_widget WHERE run_id=?)`, run).Scan(&raw); e != nil || raw != v.raw {
			t.Fatal("deactivation recomputed view", run, e)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-form-views%'`) != 0 {
		t.Fatal("pure producer left effects")
	}
}

// Child inherits the original graph without gaining Main-only publication rights.
func TestUIFormViewChildAdmissionAfterUpgradeKeepsParentGraphAndService(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.ServiceInstance(ctx, first.Revision, contract.FormViews)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := installFormViewVersion124(t, h, original.PackageDigest)
	c, run, gen, toolCalls := childAdmission(t, s, r, a, p, `{"task":"child explicit input"}`)
	released := false
	defer func() {
		if !released {
			_ = r.Release(ctx, c)
		}
	}()
	if err = h.Propose(ctx, rsi.Candidate{ID: "forms-child-upgrade", Baseline: first.Revision, Target: target, PolicyID: "ui-form-views-v124", Goal: "Child inherits parent Form view graph"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "forms-child-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "forms-child-upgrade", Expected: first.Revision, Target: target, DecisionID: decision}); err != nil {
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
	if scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.parent_run_id IS NOT NULL AND cb.graph_revision=pb.graph_revision AND cb.graph_revision=? AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?)`, first.Revision, contract.FormViews) != 2 {
		t.Fatal("Child/grandchild adopted active Form view graph or lost service fence")
	}
	var childGraph string
	if err = s.db.QueryRow(`SELECT b.graph_revision FROM rsi_run b JOIN run c USING(run_id) WHERE c.worker_position='child' ORDER BY c.rowid LIMIT 1`).Scan(&childGraph); err != nil {
		t.Fatal(err)
	}
	binding, err := h.ServiceInstance(ctx, childGraph, contract.FormViews)
	if err != nil || binding.PackageDigest != original.PackageDigest {
		t.Fatal("Child resolved upgraded package", binding, err)
	}
	raw, err := h.ReadService(ctx, childGraph, contract.FormViews, json.RawMessage(`{}`), []rsi.InputData{{ID: contract.FormViewInput, Data: []byte(`{"forms":[{"component":0,"name":"steward.form","props":{"title":"Child choices","fields":[{"name":"confirm","label":"Confirm","type":"boolean"}]}}]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	var views contract.FormViewsResult
	if rsiplugin.DecodeResult(raw, &views) != nil || len(views.Views) != 1 || len(views.Views[0].Fields) != 1 || views.Views[0].Fields[0].Default != "false" || strings.Contains(views.Views[0].Title, "Form v124:") || !strings.Contains(views.Views[0].Title, "Child choices") {
		t.Fatal("Child frozen pure service substituted", string(raw))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_form_view_snapshot`) != 0 {
		t.Fatal("worker acquired a Main UI publication")
	}
	active, err := h.Store.Status(ctx)
	if err != nil || active.Revision != target {
		t.Fatal("test repaired active selection", active, err)
	}
}
