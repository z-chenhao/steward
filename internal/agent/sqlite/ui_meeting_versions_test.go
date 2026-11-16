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
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installMeetingView126(t *testing.T, h rsi.Host, original string) (string, string) {
	t.Helper()
	ctx := context.Background()
	source := t.TempDir()
	if err := os.CopyFS(source, os.DirFS(h.Catalog.Path(original))); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	for _, name := range []string{"plugin.go", "meeting_view.go"} {
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
			code = strings.Replace(code, `Title: props.Reason`, `Title: "Meeting v126: " + props.Reason`, 1)
			code = strings.Replace(code, `DeclineLabel: "None of these work"`, `DeclineLabel: "Try other times"`, 1)
		}
		if code == string(raw) {
			t.Fatal("variant did not change implementation", name)
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
	policy.ID = "ui-meeting-v126"
	changed := 0
	for i := range policy.Cases {
		item := &policy.Cases[i]
		if item.Contract == contract.Normalize {
			var expected contract.Result
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			for j := range expected.MeetingViews {
				expected.MeetingViews[j].Title = "Meeting v126: " + expected.MeetingViews[j].Title
				expected.MeetingViews[j].DeclineLabel = "Try other times"
				changed++
			}
			item.Expected, _ = json.Marshal(expected)
		}
		if item.Contract == contract.PreviewContract {
			var expected contract.PreviewResult
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(expected.PreviewJSON, `"meeting_views"`) {
				expected.PreviewJSON = strings.Replace(expected.PreviewJSON, `"title":"Review"`, `"title":"Meeting v126: Review"`, 1)
				expected.PreviewJSON = strings.Replace(expected.PreviewJSON, `"decline_label":"None of these work"`, `"decline_label":"Try other times"`, 1)
				changed++
			}
			item.Expected, _ = json.Marshal(expected)
		}
	}
	if changed != 2 {
		t.Fatal("missing independent meeting expectations", changed)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, pkg.Digest
}

func TestUIMeetingViewIndependentVersionsRetainPendingRunPreviewAndReferences(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	host := installUIRuntimePlugin(t, store, runs)
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := host.ServiceInstance(ctx, first.Revision, contract.Normalize)
	if err != nil {
		t.Fatal(err)
	}
	target, digest := installMeetingView126(t, host, original.PackageDigest)
	choose := func(id, from, to, policy string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent original meeting display"}); e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: from, Target: to, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	provider.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Times ready."}, nil
		}
		return calls(ui.ShowName, meetingDocument), nil
	}
	var mu sync.Mutex
	previews := map[string][]string{}
	actor.Sink = func(event agent.Event) {
		if event.UIPreview != nil && len(event.UIPreview.MeetingViews) > 0 {
			mu.Lock()
			v := event.UIPreview.MeetingViews[0]
			previews[event.RunID] = append(previews[event.RunID], v.Title+"|"+v.DeclineLabel)
			mu.Unlock()
		}
	}
	type saved struct{ ref, raw, graph string }
	history := map[string]saved{}
	assert := func(run, pkg string, variant bool) {
		t.Helper()
		var value saved
		if e := store.db.QueryRow(`SELECT w.ui_ref,c.result_json,b.graph_revision FROM ui_widget w JOIN rsi_call c ON c.run_id=w.run_id AND c.operation_id='ui-normalize:'||w.invocation_id JOIN rsi_run b ON b.run_id=w.run_id WHERE w.run_id=?`, run).Scan(&value.ref, &value.raw, &value.graph); e != nil {
			t.Fatal(e)
		}
		binding, e := host.ServiceInstance(ctx, value.graph, contract.Normalize)
		if e != nil || binding.PackageDigest != pkg {
			t.Fatal("substituted package", e)
		}
		component := deliveredMeetingForRun(t, store, run)
		view := component.MeetingView
		title, label := "Review", "None of these work"
		if variant {
			title, label = "Meeting v126: Review", "Try other times"
		}
		if view.Title != title || view.DeclineLabel != label || view.Slots[1].Choice != 1 {
			t.Fatal("substituted owning facts", view)
		}
		mu.Lock()
		pictures := append([]string(nil), previews[run]...)
		mu.Unlock()
		if len(pictures) == 0 {
			t.Fatal("no actual preview")
		}
		for _, picture := range pictures {
			if picture != title+"|"+label {
				t.Fatal("preview used current selection", picture)
			}
		}
		history[run] = value
	}
	old := mustSubmit(t, actor, "meeting-old-pending", "Show old times")
	choose("meeting-upgrade", first.Revision, target, "ui-meeting-v126")
	mustDrain(t, actor)
	assert(old.RunID, original.PackageDigest, false)
	current := mustSubmit(t, actor, "meeting-current", "Show current times")
	mustDrain(t, actor)
	assert(current.RunID, digest, true)
	pending := mustSubmit(t, actor, "meeting-new-pending", "Keep current times")
	choose("meeting-rollback", target, first.Revision, "ui-runtime-fixture")
	mustDrain(t, actor)
	assert(pending.RunID, digest, true)
	rolled := mustSubmit(t, actor, "meeting-rolled", "Show rolled times")
	mustDrain(t, actor)
	assert(rolled.RunID, original.PackageDigest, false)
	if _, err = host.Store.Deactivate(ctx, "meeting-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	runs.plugins = nil
	for run, value := range history {
		component := deliveredMeetingForRun(t, store, run)
		if component.MeetingView == nil {
			t.Fatal("deactivation lost original reference")
		}
		var after string
		if e := store.db.QueryRow(`SELECT result_json FROM rsi_call WHERE run_id=? AND operation_id LIKE 'ui-normalize:%'`, run).Scan(&after); e != nil || after != value.raw {
			t.Fatal("deactivation rewrote accepted display", e)
		}
	}
}

// The real consumer is the authorized conversation projection, not the sandbox
// widget endpoint (which intentionally rejects native components).
func deliveredMeetingForRun(t *testing.T, store *Store, run string) ui.Component {
	t.Helper()
	ctx := context.Background()
	var rawMessage string
	if err := store.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.run_id=?`, run).Scan(&rawMessage); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err := json.Unmarshal([]byte(rawMessage), &message); err != nil {
		t.Fatal(err)
	}
	raw, err := store.WebProjection(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if _, err = store.WebBubbles(ctx, message, spec); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Components {
		if c.Name == "scheduleTime" && c.MeetingManaged && c.MeetingView != nil {
			return c
		}
	}
	t.Fatal("authorized projection lost original managed meeting")
	return ui.Component{}
}

func TestUIMeetingViewChildAdmissionKeepsOriginalNormalizerGraph(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.ServiceInstance(ctx, first.Revision, contract.Normalize)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := installMeetingView126(t, h, original.PackageDigest)
	c, run, gen, toolCalls := childAdmission(t, s, r, a, p, `{"task":"child explicit input"}`)
	released := false
	defer func() {
		if !released {
			_ = r.Release(ctx, c)
		}
	}()
	if err = h.Propose(ctx, rsi.Candidate{ID: "meeting-child-upgrade", Baseline: first.Revision, Target: target, PolicyID: "ui-meeting-v126", Goal: "Child inherits parent meeting display graph"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, "meeting-child-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: "meeting-child-upgrade", Expected: first.Revision, Target: target, DecisionID: decision}); err != nil {
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
	if scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.parent_run_id IS NOT NULL AND cb.graph_revision=pb.graph_revision AND cb.graph_revision=? AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?)`, first.Revision, contract.Normalize) != 2 {
		t.Fatal("Child/grandchild adopted active meeting display graph or lost service fence")
	}
	var childGraph string
	if err = s.db.QueryRow(`SELECT b.graph_revision FROM rsi_run b JOIN run c USING(run_id) WHERE c.worker_position='child' ORDER BY c.rowid LIMIT 1`).Scan(&childGraph); err != nil {
		t.Fatal(err)
	}
	binding, err := h.ServiceInstance(ctx, childGraph, contract.Normalize)
	if err != nil || binding.PackageDigest != original.PackageDigest {
		t.Fatal("Child resolved upgraded package", binding, err)
	}
	raw, err := h.ReadService(ctx, childGraph, contract.Normalize, json.RawMessage(`{}`), []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(meetingDocument)}})
	if err != nil {
		t.Fatal(err)
	}
	var result contract.Result
	if rsiplugin.DecodeResult(raw, &result) != nil || len(result.MeetingViews) != 1 || result.MeetingViews[0].Title != "Review" || result.MeetingViews[0].DeclineLabel != "None of these work" {
		t.Fatal("Child substituted current meeting display", string(raw))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_form_view_snapshot`) != 0 {
		t.Fatal("worker acquired a Main UI publication")
	}
	active, err := h.Store.Status(ctx)
	if err != nil || active.Revision != target {
		t.Fatal("test repaired active selection", active, err)
	}
}
