package sqlite

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestUIManagedIndependentUpgradeRollbackRetainsOldDeclarationsAndReadable(t *testing.T) {
	ctx := context.Background()
	defs, err := skill.IncludeIntelligentUI(skill.EmptyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	defs = append(defs, ui.ShowDefinition())
	s, r, a, p := childFixture(t, defs...)
	inputProfile := modelInput()
	inputProfile.ContextWindow = 65536
	activateLoop(t, s, inputProfile)
	if err = r.ConfigureSkillLibrary(); err != nil {
		t.Fatal(err)
	}
	h := installUIRuntimePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.ServiceInstance(ctx, first.Revision, contract.Normalize)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err = os.CopyFS(source, os.DirFS(h.Catalog.Path(original.PackageDigest))); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	for _, file := range []string{"plugin.go", "definition.go", "summary.go", "preview.go", "forms.go"} {
		path, err := filepath.Abs("../../plugin/intelligentui/" + file)
		if err != nil {
			t.Fatal(err)
		}
		code, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		updated := string(code)
		if file == "plugin.go" {
			updated = strings.Replace(updated, `Version: "1.27.0"`, `Version: "1.28.0"`, 1)
			updated = strings.Replace(updated, `Readable: widget.Readable()`, `Readable: "v12: " + widget.Readable()`, 1)
		} else if file == "definition.go" {
			updated = strings.Replace(updated, `Description: "Show visuals;`, `Description: "v12: Show visuals;`, 1)
		} else if file == "summary.go" {
			updated = strings.Replace(updated, `return strings.TrimSpace(strings.Join(lines, "\n"))`, `return "v16: <script>PRIVATE_VARIANT()</script> " + strings.TrimSpace(strings.Join(lines, "\n"))`, 1)
		} else if file == "preview.go" {
			updated = strings.Replace(updated, "return out, true", `out.Title = "v18: " + out.Title; return out, true`, 1)
		} else {
			updated = strings.Replace(updated, "out.Parts = parts", `parts[0] = "v110: " + parts[0]; out.Parts = parts`, 1)
		}
		if updated == string(code) {
			t.Fatal("version fixture failed to change actual implementation", file)
		}
		replacement := filepath.Join(t.TempDir(), file)
		if err = os.WriteFile(replacement, []byte(updated), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = replacement
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
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
	skillPath := filepath.Join(source, "skill/intelligent_ui/SKILL.md")
	skillBytes, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	skillText := strings.Replace(string(skillBytes), "description: Present a compact native component or interactive visual when it makes an answer easier to understand, compare, or explore.", "description: Version fourteen independent UI methods.", 1)
	if skillText == string(skillBytes) {
		t.Fatal("Skill metadata fixture did not change")
	}
	if err = os.WriteFile(skillPath, append([]byte(skillText), []byte("\nINDEPENDENT_UI_SKILL_V14\n")...), 0600); err != nil {
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
	policy.ID = "ui-runtime-v12"
	// Apply this independently defined variant to every matching Owner case,
	// including the later meeting/action contributions. Case order is not a
	// contract, and presentation strings retain the producer's original shape.
	for i := range policy.Cases {
		item := &policy.Cases[i]
		switch item.Contract {
		case contract.Normalize:
			var expected contract.Result
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if expected.ErrorCode != "" {
				continue
			}
			expected.Readable = "v12: " + expected.Readable
			var document struct {
				Components []struct {
					Name string `json:"name"`
				} `json:"components"`
			}
			if err = json.Unmarshal([]byte(expected.DocumentJSON), &document); err != nil {
				t.Fatal(err)
			}
			for j, c := range document.Components {
				if c.Name != "generateSandboxedUi" && !strings.HasPrefix(c.Name, "steward.") {
					expected.ComponentText[j] = "v16: <script>PRIVATE_VARIANT()</script> " + expected.ComponentText[j]
				}
			}
			item.Expected, _ = json.Marshal(expected)
		case contract.DefinitionContract:
			var expected contract.Definition
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			expected.Description = "v12: " + expected.Description
			item.Expected, _ = json.Marshal(expected)
		case contract.PreviewContract:
			var expected contract.PreviewResult
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if !expected.Valid {
				continue
			}
			var preview struct {
				MeetingViews []contract.MeetingView `json:"meeting_views,omitempty"`
				Title        string                 `json:"title"`
				Text         string                 `json:"text,omitempty"`
				Components   []struct {
					Name  string          `json:"name,omitempty"`
					Props json.RawMessage `json:"props,omitempty"`
				} `json:"components"`
			}
			if err = json.Unmarshal([]byte(expected.PreviewJSON), &preview); err != nil {
				t.Fatal(err)
			}
			preview.Title = "v18: " + preview.Title
			wire, _ := json.Marshal(preview)
			expected.PreviewJSON = string(wire)
			item.Expected, _ = json.Marshal(expected)
		case contract.FormContract:
			var expected contract.FormResult
			if err = json.Unmarshal(item.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if expected.ErrorCode == "" && len(expected.Parts) > 0 {
				expected.Parts[0] = "v110: " + expected.Parts[0]
			}
			item.Expected, _ = json.Marshal(expected)
		}
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	choose := func(id, baseline, target, policyID string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policyID, Goal: "independent UI wording and frozen declarations"}); err != nil {
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
	input := `{"title":"Versioned","fallback":"Versioned 42","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}},{"name":"steward.form","props":{"title":"Versioned answer","fields":[{"name":"answer","label":"Answer","type":"textarea","required":true}]}}]}`
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Versioned reply."}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "show", Name: ui.ShowName, Arguments: input}, {ID: "skill", Name: "builtin__skill_view", Arguments: `{"name":"intelligent_ui"}`}, {ID: "list", Name: "builtin__skill_list", Arguments: `{"query":"","limit":8}`}}}, nil
	}
	var previewMu sync.Mutex
	previewTitles := map[string][]string{}
	a.Sink = func(event agent.Event) {
		if event.Kind == "ui_preview_update" && event.UIPreview != nil {
			previewMu.Lock()
			previewTitles[event.RunID] = append(previewTitles[event.RunID], event.UIPreview.Title)
			previewMu.Unlock()
		}
	}
	old := mustSubmit(t, a, "ui-version-old", "Show old frozen UI")
	choose("ui-upgrade", first.Revision, graph.Revision, policy.ID)
	mustDrain(t, a)
	assertVersion := func(run, digest, prefix string) {
		t.Helper()
		previewMu.Lock()
		titles := append([]string(nil), previewTitles[run]...)
		previewMu.Unlock()
		if len(titles) == 0 {
			t.Fatal("managed preview service was not observed", run)
		}
		for _, title := range titles {
			if strings.HasPrefix(title, "v18: ") != (prefix != "") {
				t.Fatal("preview substituted current/native implementation", run, title)
			}
		}
		var declaration, readable, delivered string
		err := s.db.QueryRow(`SELECT json_extract(t.value,'$.description') FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) t WHERE r.run_id=? AND json_extract(t.value,'$.name')='builtin__intelligent_ui_show' AND json_extract(t.value,'$.resource_hash')=?`, run, digest).Scan(&declaration)
		if err != nil || strings.HasPrefix(declaration, "v12: ") != (prefix != "") {
			t.Fatal("wrong frozen declaration", declaration, err)
		}
		err = s.db.QueryRow(`SELECT json_extract(c.result_json,'$.readable'),m.message_json FROM ui_widget w JOIN rsi_call c ON c.run_id=w.run_id AND c.operation_id='ui-normalize:'||w.invocation_id JOIN conversation_intent m ON m.message_id=w.message_id WHERE w.run_id=?`, run).Scan(&readable, &delivered)
		if err != nil || strings.HasPrefix(readable, "v12: ") != (prefix != "") || !strings.Contains(delivered, readable) {
			t.Fatal("wrong accepted or delivered wording", readable, delivered, err)
		}
		var message conversation.Message
		if err := json.Unmarshal([]byte(delivered), &message); err != nil {
			t.Fatal(err)
		}
		projected, err := s.WebProjection(ctx, message)
		if err != nil {
			t.Fatal(err)
		}
		staticHTML := string(ui.HTML(projected, "Versioned 42"))
		if strings.Contains(staticHTML, "<script>") {
			t.Fatal("candidate component text became Host executable HTML")
		}
		if strings.Contains(staticHTML, "v16: &lt;script&gt;PRIVATE_VARIANT()&lt;/script&gt;") != (prefix != "") {
			t.Fatal("HTML fallback substituted Host/current summary", staticHTML)
		}
		var spec ui.Spec
		if json.Unmarshal(projected, &spec) != nil {
			t.Fatal("invalid projection")
		}
		found := false
		for _, component := range spec.Components {
			if component.Name == "Table" {
				found = true
				if strings.HasPrefix(component.Text, "v16: ") != (prefix != "") {
					t.Fatal("component projection substituted Host summary", component.Text)
				}
			}
		}
		if !found {
			t.Fatal("missing actual component projection")
		}
		var skillResult string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__skill_view' AND status='succeeded'`, run).Scan(&skillResult); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(skillResult, "INDEPENDENT_UI_SKILL_V14") != (prefix != "") {
			t.Fatal("Skill reader substituted current version", run, skillResult)
		}
		var indexText, listResult string
		if err := s.db.QueryRow(`SELECT x.value FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) t,json_each(t.value,'$.prompt_context') p,json_each(json_extract(p.value,'$.text')) x WHERE r.run_id=? AND json_extract(t.value,'$.name')='builtin__skill_list' AND json_extract(p.value,'$.kind')='skill_index' AND json_extract(x.value,'$.name')='intelligent_ui'`, run).Scan(&indexText); err != nil {
			t.Fatal(err)
		}
		var entry skill.Summary
		if json.Unmarshal([]byte(indexText), &entry) != nil || entry.Version != digest || strings.HasPrefix(entry.Description, "Version fourteen") != (prefix != "") {
			t.Fatal("wrong frozen Skill summary", entry)
		}
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__skill_list' AND status='succeeded'`, run).Scan(&listResult); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(listResult, "Version fourteen") != (prefix != "") {
			t.Fatal("Skill listing substituted live summary", listResult)
		}
	}
	assertVersion(old.RunID, original.PackageDigest, "")
	current := mustSubmit(t, a, "ui-version-new", "Show new UI")
	mustDrain(t, a)
	assertVersion(current.RunID, pkg.Digest, "v12: ")
	retained := mustSubmit(t, a, "ui-version-retained", "Retain new pending UI")
	choose("ui-rollback", graph.Revision, first.Revision, "ui-runtime-fixture")
	// Resume is immediate after rollback, without selection or fixture repair.
	mustDrain(t, a)
	assertVersion(retained.RunID, pkg.Digest, "v12: ")
	rolled := mustSubmit(t, a, "ui-version-rolled", "Show rolled back UI")
	mustDrain(t, a)
	assertVersion(rolled.RunID, original.PackageDigest, "")
	assertVersion(old.RunID, original.PackageDigest, "")
	assertVersion(current.RunID, pkg.Digest, "v12: ")
	if _, err = s.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	type acceptedForm struct {
		message   conversation.Message
		component ui.Component
		content   []conversation.ContentPart
		response  string
	}
	var accepted []acceptedForm
	// Current selection is rolled back. Each form must still execute its own
	// published Run's producer, including the new package retained above.
	for _, tc := range []struct{ run, prefix string }{{old.RunID, ""}, {current.RunID, "v110: "}, {retained.RunID, "v110: "}, {rolled.RunID, ""}} {
		var raw string
		if err = s.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.run_id=?`, tc.run).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var m conversation.Message
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		projected, err := s.WebProjection(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		var spec ui.Spec
		if err = json.Unmarshal(projected, &spec); err != nil {
			t.Fatal(err)
		}
		var form ui.Component
		for _, c := range spec.Components {
			if c.FormID != "" {
				form = c
			}
		}
		if form.FormID == "" {
			t.Fatal("real versioned form absent", tc.run)
		}
		receipt, err := r.AcceptWebForm(ctx, m, form.FormID, map[string]string{"answer": "Version-frozen answer"})
		if err != nil {
			t.Fatal(err)
		}
		content, found, err := s.ChannelSourceContent(ctx, "web", web.Subject, web.Destination, "ui-form:"+form.FormID)
		if err != nil || !found || len(content) != 2 || content[0].Text != tc.prefix+"Response to: Versioned answer\n" || content[1].Text != "Answer: Version-frozen answer\n" {
			t.Fatal("form substituted current/native policy", tc.run, content, found, err)
		}
		retry, err := r.AcceptWebForm(ctx, m, form.FormID, map[string]string{"answer": "Version-frozen answer"})
		if err != nil || retry != receipt {
			t.Fatal("versioned answer retry changed receipt", retry, receipt, err)
		}
		accepted = append(accepted, acceptedForm{m, form, content, `{"answer":"Version-frozen answer"}`})
	}
	if _, err = h.Store.Deactivate(ctx, "ui-version-forms-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	// Dependent restoration immediately follows deactivation; no reselect,
	// reload or fixture mutation repairs the original version fences.
	for _, f := range accepted {
		if err = s.RestoreWebForm(ctx, f.message, &f.component, f.content); err != nil || !f.component.Submitted || f.component.Response != f.response {
			t.Fatal("versioned form cannot restore without candidate", f.component, err)
		}
	}

}
