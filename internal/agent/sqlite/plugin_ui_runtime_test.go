package sqlite

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/llm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installUIRuntimePlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	return installUIRuntimeAtTableBoundary(t, s, r, false)
}

func installUIRuntimeAtTableBoundary(t *testing.T, s *Store, r *Runs, historical bool, charts ...bool) rsi.Host {
	return installUIRuntimeAtFormBoundary(t, s, r, historical, false, charts...)
}

func installUIRuntimeAtFormBoundary(t *testing.T, s *Store, r *Runs, historical, formViews bool, charts ...bool) rsi.Host {
	t.Helper()
	source := t.TempDir()
	if historical {
		args := []string{"build", "-o", filepath.Join(source, "plugin")}
		path, err := filepath.Abs("../../plugin/intelligentui/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(raw), `Version: "1.27.0"`, `Version: "1.13.0"`, 1)
		code = strings.Replace(code, `contract.TableViews: tableViews, `, "", 1)
		code = strings.Replace(code, `contract.ChartViews: chartViews, `, "", 1)
		code = strings.Replace(code, `contract.FormViews: formViews, `, "", 1)
		if strings.Contains(code, `contract.TableViews: tableViews`) || strings.Contains(code, `contract.ChartViews: chartViews`) {
			t.Fatal("historical handler boundary not removed")
		}
		replacement := filepath.Join(t.TempDir(), "plugin.go.txt")
		if err = os.WriteFile(replacement, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: replacement}})
		overlayPath := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-overlay", overlayPath, "../../../cmd/plugins/intelligent-ui")
		if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	} else {
		buildTestBinary(t, "../../../cmd/plugins/intelligent-ui", filepath.Join(source, "plugin"))
	}
	if err := os.Mkdir(filepath.Join(source, "view"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"widget-runtime.js", "widget.css"} {
		raw, err := os.ReadFile("../../interaction/web/assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "view", name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(filepath.Join(source, "skill"), os.DirFS("../../plugin/intelligentui/skill")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../plugins/intelligent-ui/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	if historical {
		manifest.Version = "1.13.0"
		services := manifest.Provides[:0]
		for _, service := range manifest.Provides {
			if service.Contract != contract.TableViews && service.Contract != contract.ChartViews && service.Contract != contract.FormViews {
				services = append(services, service)
			}
		}
		manifest.Provides = services
	}
	chartDeclared, formDeclared := false, false
	for _, service := range manifest.Provides {
		chartDeclared = chartDeclared || service.Contract == contract.ChartViews
		formDeclared = formDeclared || service.Contract == contract.FormViews
	}
	if len(charts) > 0 && charts[0] && !chartDeclared {
		manifest.Provides = append(manifest.Provides, rsiplugin.Service{Contract: contract.ChartViews, InputSchema: json.RawMessage(contract.ChartViewsInputSchema), OutputSchema: json.RawMessage(contract.ChartViewsOutputSchema)})
	}
	if formViews && !historical && !formDeclared {
		manifest.Provides = append(manifest.Provides, rsiplugin.Service{Contract: contract.FormViews, InputSchema: json.RawMessage(contract.FormViewsInputSchema), OutputSchema: json.RawMessage(contract.FormViewsOutputSchema)})
	}
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Expected interpretation comes from the original native consumer, not the
	// candidate. Independent lifecycle and business dispatch are asserted below.
	input := `{"title":"Fixture","fallback":"Fixture","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	widget, ok := ui.ParseWidget(input)
	if !ok {
		t.Fatal("invalid original fixture")
	}
	document, _ := json.Marshal(widget)
	projection := ui.WidgetComponents(widget, "ui:oracle", "oracle", 0)
	text := make([]string, len(projection))
	for i, c := range projection {
		text[i] = c.Text
	}
	expected, _ := json.Marshal(contract.Result{DocumentJSON: string(document), Readable: widget.Readable(), ComponentText: text})
	policy := rsi.Policy{ID: "ui-runtime-fixture", AllowedContracts: []string{contract.Normalize, contract.DefinitionContract, contract.PreviewContract, contract.FormContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "intelligent-ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(input)}}, Expected: expected}}}

	original := ui.ShowDefinition().Spec
	runtimeBytes, err := os.ReadFile(filepath.Join(source, contract.RuntimeFile))
	if err != nil {
		t.Fatal(err)
	}
	stylesBytes, err := os.ReadFile(filepath.Join(source, contract.StylesFile))
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := json.Marshal(contract.Definition{InputSchema: original.InputSchema, ModelInputSchema: original.ModelInputSchema, Description: original.Description, Runtime: contract.ViewResource{Name: contract.RuntimeResource, Digest: rsiplugin.FileDigest(runtimeBytes)}, Styles: contract.ViewResource{Name: contract.StylesResource, Digest: rsiplugin.FileDigest(stylesBytes)}})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.DefinitionContract, Input: json.RawMessage(`{}`), Expected: definition})
	rawPreview := `{"title":"Preview","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"html":"<p>42","jsFunctions":"PRIVATE_CODE","jsExpressions":["PRIVATE_CODE()"]}}]}`
	originalPreview, validPreview := ui.ParsePreview(rawPreview)
	originalPreviewJSON, _ := json.Marshal(originalPreview)
	expectedPreview, _ := json.Marshal(contract.PreviewResult{Valid: validPreview, PreviewJSON: string(originalPreviewJSON)})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.PreviewContract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(rawPreview)}}, Expected: expectedPreview})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.FormContract, Input: json.RawMessage(`{"input_id":"ui-form"}`), Inputs: []rsi.InputData{{ID: contract.FormInput, Data: []byte(`{"name":"scheduleTime","props":{"reasonForScheduling":"Interview","meetingDuration":45,"timeSlots":[{"date":"2026-11-16","time":"10:00"},{"date":"2026-11-17","time":"11:00"}]},"values":{"choice":"0"}}`)}}, Expected: json.RawMessage(`{"parts":["Meeting time preference for Interview: 2026-11-16 at 10:00 (45 min). This selects a preferred time; it does not book a meeting."],"response":"0","error_code":""}`)})
	{
		policy.AllowedContracts = append(policy.AllowedContracts, contract.PlanViews)
		raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-plan.json")
		if err != nil {
			t.Fatal(err)
		}
		var cases []struct {
			Expected struct {
				HTML string `json:"html"`
			} `json:"expected"`
		}
		if err = json.Unmarshal(raw, &cases); err != nil {
			t.Fatal(err)
		}
		css, err := os.ReadFile("../../plugin/intelligentui/testdata/original-plan.css")
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(contract.PlanViewsResult{Views: []contract.PlanView{{Component: 0, HTML: cases[0].Expected.HTML, CSS: strings.Replace(string(css), "@media(max-width:760px){.native-plan summary{min-height:44px}}", "html[data-host-compact] .native-plan summary{min-height:44px}", 1), CompactMaxWidth: 760}}})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.PlanViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.PlanInput, Data: []byte(`{"plans":[{"component":0,"props":{"approach":"Ship core","technology":"Go","key_elements":["Loop","Host"]}}]}`)}}, Expected: expected})
	}
	for _, service := range manifest.Provides {
		if service.Contract != contract.CardsViews {
			continue
		}
		policy.AllowedContracts = append(policy.AllowedContracts, contract.CardsViews)
		raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-cards.json")
		if err != nil {
			t.Fatal(err)
		}
		var original struct {
			Props json.RawMessage `json:"props"`
			HTML  string          `json:"initial_html"`
		}
		if err = json.Unmarshal(raw, &original); err != nil {
			t.Fatal(err)
		}
		runtime, err := os.ReadFile("../../plugin/intelligentui/view/cards-worker.js")
		if err != nil {
			t.Fatal(err)
		}
		css, err := os.ReadFile("../../plugin/intelligentui/view/cards.css")
		if err != nil {
			t.Fatal(err)
		}
		// Reviewed Owner transport oracle is independent of candidate output.
		oracleRaw, err := os.ReadFile("../../rsi/builtin/ui-cards-worker.json")
		if err != nil {
			t.Fatal(err)
		}
		var oracle struct {
			Expected contract.CardsViewsResult `json:"expected"`
		}
		if err = json.Unmarshal(oracleRaw, &oracle); err != nil || len(oracle.Expected.Views) != 1 {
			t.Fatal("invalid Cards Owner oracle", err)
		}
		original.HTML = oracle.Expected.Views[0].HTML
		facts, _ := json.Marshal(contract.CardsFacts{Cards: []contract.CardsFact{{Component: 1, Props: original.Props}}})
		expected, _ := json.Marshal(contract.CardsViewsResult{Views: []contract.CardsView{{Component: 1, HTML: original.HTML, Worker: string(runtime), CSS: string(css), CompactMaxWidth: 760, Contexts: []contract.CardsContext{{Title: "Card choices", VisibleItem: "Alpha"}, {Title: "Card choices", VisibleItem: "Beta"}}}}})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.CardsViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.CardsInput, Data: facts}}, Expected: expected})
	}

	for _, service := range manifest.Provides {
		if service.Contract != contract.TableViews {
			continue
		}
		raw, err := os.ReadFile("../../rsi/builtin/ui-table-original.json")
		if err != nil {
			t.Fatal(err)
		}
		var oracle struct {
			Facts    json.RawMessage `json:"facts"`
			Expected json.RawMessage `json:"expected"`
		}
		if err = json.Unmarshal(raw, &oracle); err != nil {
			t.Fatal(err)
		}
		policy.AllowedContracts = append(policy.AllowedContracts, contract.TableViews)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.TableViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.TableInput, Data: oracle.Facts}}, Expected: oracle.Expected})
	}
	if chartDeclared || len(charts) > 0 && charts[0] {
		worker, err := os.ReadFile("../../plugin/intelligentui/view/charts-worker.js")
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(contract.ChartViewsResult{Views: []contract.ChartView{{Component: 1, Worker: string(worker), Title: "Trend", Description: "Original", Columns: []contract.ChartColumn{{Header: "Label", Source: "label"}, {Header: "Value", Source: "value"}}}}})
		policy.AllowedContracts = append(policy.AllowedContracts, contract.ChartViews)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.ChartViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.ChartInput, Data: []byte(`{"charts":[{"component":1,"name":"steward.lineChart","props":{"title":"Trend","description":"Original","data":[{"label":"A","value":10}]}}]}`)}}, Expected: expected})
	}
	if formDeclared || formViews && !historical {
		policy.AllowedContracts = append(policy.AllowedContracts, contract.FormViews)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.FormViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.FormViewInput, Data: []byte(`{"forms":[{"component":1,"name":"steward.form","props":{"title":"Original form","fields":[{"name":"notes","label":"Notes","type":"textarea"},{"name":"confirm","label":"Confirm","type":"boolean"}]}}]}`)}}, Expected: json.RawMessage(`{"views":[{"component":1,"title":"Original form","submit_label":"","fields":[{"name":"notes","label":"Notes","type":"textarea","default":""},{"name":"confirm","label":"Confirm","type":"boolean","default":"false"}]}],"error_code":""}`)})
	}
	if contract.MeetingDisplayRequired(manifest.Version) {
		data, err := os.ReadFile("../../rsi/builtin/ui-meeting-original.json")
		if err != nil {
			t.Fatal(err)
		}
		var samples []struct {
			Contract string          `json:"contract"`
			Document string          `json:"document"`
			Expected json.RawMessage `json:"expected"`
		}
		if err = json.Unmarshal(data, &samples); err != nil {
			t.Fatal(err)
		}
		for _, sample := range samples {
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: sample.Contract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
		}
	}
	if contract.ActionDisplayRequired(manifest.Version) {
		data, err := os.ReadFile("../../rsi/builtin/ui-action-original.json")
		if err != nil {
			t.Fatal(err)
		}
		var samples []struct {
			Document string          `json:"document"`
			Expected json.RawMessage `json:"expected"`
		}
		if err = json.Unmarshal(data, &samples); err != nil {
			t.Fatal(err)
		}
		for _, sample := range samples {
			policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
		}
	}
	return installRuntimePackage(t, s, r, "intelligent-ui", policy, source)
}

func TestUIManagedCorrectionRecoversAfterReceiptCommitFailure(t *testing.T) {
	testUICorrectionRecovery(t, true)
}

func assertUIFrozenPluginDefinition(t *testing.T, s *Store) {
	t.Helper()
	if got := scalar(t, s, `SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) t WHERE r.kind='main' AND json_extract(t.value,'$.name')='builtin__intelligent_ui_show' AND json_extract(t.value,'$.resource_hash') IN (SELECT digest FROM rsi_package WHERE json_extract(manifest_json,'$.plugin_id')='steward.intelligent-ui') AND json_extract(t.value,'$.permission')='ui_present' AND json_extract(t.value,'$.replay_policy')='safe'`); got != 1 {
		t.Fatal("UI definition did not freeze owning package and Host authority", got)
	}
}

func TestUIManagedDeactivationKeepsFrozenRunAndRemovesNewCapability(t *testing.T) {
	ctx := context.Background()
	defs, err := skill.IncludeIntelligentUI(skill.EmptyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	defs = append(defs, ui.ShowDefinition())
	s, r, a, p := childFixture(t, defs...)
	if err = r.ConfigureSkillLibrary(); err != nil {
		t.Fatal(err)
	}
	h := installUIRuntimePlugin(t, s, r)
	input := `{"title":"Frozen","fallback":"Frozen 42","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	active := true
	step := 0
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if !active {
			for _, spec := range req.Tools {
				if spec.Name == ui.ShowName {
					t.Error("deactivated UI advertised to new Run")
				}
			}
			return llm.GenerationResult{Text: "Plain reply after deactivation."}, nil
		}
		step++
		if step == 1 {
			return calls(ui.ShowName, input), nil
		}
		return llm.GenerationResult{Text: "Frozen reply."}, nil
	}
	old := mustSubmit(t, a, "ui-before-disable", "Show before deactivation")
	var frozen string
	if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, old.RunID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "ui-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	// First operation after deactivation is the original Run's actual resume.
	mustDrain(t, a)
	if got := scalar(t, s, `SELECT count(*) FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE w.message_id IS NOT NULL AND i.status='succeeded'`); got != 1 {
		t.Fatal("frozen Run lost UI after deactivation", got)
	}
	if got := scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='intelligent-ui' AND result_json IS NOT NULL`); got != 2 {
		t.Fatal("old graph did not dispatch original UI services", got)
	}
	var after string
	if err = s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, old.RunID).Scan(&after); err != nil || after != frozen {
		t.Fatal("deactivation changed old capability", err)
	}
	active = false
	next := mustSubmit(t, a, "ui-after-disable", "Reply after deactivation")
	mustDrain(t, a)
	var indexCount int
	if err = s.db.QueryRow(`SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) t,json_each(t.value,'$.prompt_context') p,json_each(json_extract(p.value,'$.text')) x WHERE r.run_id=? AND json_extract(t.value,'$.name')='builtin__skill_list' AND json_extract(p.value,'$.kind')='skill_index' AND json_extract(x.value,'$.name')='intelligent_ui'`, next.RunID).Scan(&indexCount); err != nil || indexCount != 0 {
		t.Fatal("native UI Skill restored after deactivation", indexCount, err)
	}
	if next.RunID == old.RunID {
		t.Fatal("fixture did not create a new Run")
	}
	var count int
	err = s.db.QueryRow(`SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) t WHERE r.run_id=? AND json_extract(t.value,'$.name')='builtin__intelligent_ui_show'`, next.RunID).Scan(&count)
	if err != nil || count != 0 {
		t.Fatal("native UI restored to new capability", count, err)
	}
	managed, err := r.managedRunService(ctx, next.RunID, contract.Normalize)
	if err != nil || !managed {
		t.Fatal("deactivation lost durable migration boundary", managed, err)
	}
	if got := scalar(t, s, `SELECT count(*) FROM ui_widget`); got != 1 {
		t.Fatal("deactivation replayed UI", got)
	}
}
