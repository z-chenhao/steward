package rsi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
)

func TestUIDocumentPluginIsolatedMatchesOriginalNativeOracle(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	source := t.TempDir()
	command := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/intelligent-ui")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	data, err := os.ReadFile("../../plugins/intelligent-ui/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(source, "view"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"widget-runtime.js", "widget.css"} {
		raw, e := os.ReadFile(filepath.Join("../interaction/web/assets", name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "view", name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if err := os.CopyFS(filepath.Join(source, "skill"), os.DirFS("../plugin/intelligentui/skill")); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newFixture(t)
	pkg, err := fixture.host.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := fixture.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "ui", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "ui", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("../plugin/intelligentui/testdata/original-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string          `json:"name"`
		Input      string          `json:"input"`
		Accepted   bool            `json:"accepted"`
		Normalized json.RawMessage `json:"normalized"`
		Readable   string          `json:"readable"`
		Components []struct {
			Text string `json:"text"`
		} `json:"components"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 18 {
		t.Fatal("lost original native cases")
	}
	policy := rsi.Policy{ID: "ui-original-document", AllowedContracts: []string{contract.Normalize, contract.DefinitionContract, contract.PreviewContract, contract.FormContract, contract.PlanViews, contract.CardsViews, contract.TableViews, contract.ChartViews, contract.FormViews}, AllowedRequests: []string{"input.read"}}
	meetingOracle, err := os.ReadFile("../plugin/intelligentui/testdata/original-meeting-views.json")
	if err != nil {
		t.Fatal(err)
	}
	var meetingCases map[string][]contract.MeetingView
	if err = json.Unmarshal(meetingOracle, &meetingCases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		expected := contract.Result{ErrorCode: "INVALID_ARGUMENT"}
		if c.Accepted {
			var original bytes.Buffer
			if err = json.Compact(&original, c.Normalized); err != nil {
				t.Fatal(err)
			}
			text := make([]string, len(c.Components))
			for i, v := range c.Components {
				text[i] = v.Text
			}
			expected = contract.Result{DocumentJSON: original.String(), Readable: c.Readable, ComponentText: text, MeetingViews: meetingCases[c.Name]}
		}
		raw, _ := json.Marshal(expected)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(c.Input)}}, Expected: raw})
	}
	original := ui.ShowDefinition().Spec
	runtimeBytes, err := os.ReadFile(filepath.Join(source, contract.RuntimeFile))
	if err != nil {
		t.Fatal(err)
	}
	stylesBytes, err := os.ReadFile(filepath.Join(source, contract.StylesFile))
	if err != nil {
		t.Fatal(err)
	}
	expectedDefinition, _ := json.Marshal(contract.Definition{InputSchema: original.InputSchema, ModelInputSchema: original.ModelInputSchema, Description: original.Description, Runtime: contract.ViewResource{Name: contract.RuntimeResource, Digest: rsiplugin.FileDigest(runtimeBytes)}, Styles: contract.ViewResource{Name: contract.StylesResource, Digest: rsiplugin.FileDigest(stylesBytes)}})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.DefinitionContract, Input: json.RawMessage(`{}`), Expected: expectedDefinition})
	rawPreview := `{"title":"Preview","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"html":"<p>42","jsFunctions":"PRIVATE_CODE","jsExpressions":["PRIVATE_CODE()"]}}]}`
	originalPreview, validPreview := ui.ParsePreview(rawPreview)
	originalPreviewJSON, _ := json.Marshal(originalPreview)
	expectedPreview, _ := json.Marshal(contract.PreviewResult{Valid: validPreview, PreviewJSON: string(originalPreviewJSON)})
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.PreviewContract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(rawPreview)}}, Expected: expectedPreview})
	// Original form outputs are captured independently of the candidate. Only
	// answer is an RPC: restoration reads the atomically accepted Host fact.
	formData, err := os.ReadFile("../plugin/intelligentui/testdata/original-form.json")
	if err != nil {
		t.Fatal(err)
	}
	var forms []struct {
		Input struct {
			contract.FormFacts
			Operation string `json:"operation"`
		} `json:"input"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(formData, &forms); err != nil {
		t.Fatal(err)
	}
	if len(forms) != 31 {
		t.Fatal("lost native form corpus")
	}
	formCases := 0
	for _, c := range forms {
		if c.Input.Operation != "answer" {
			continue
		}
		facts, err := json.Marshal(c.Input.FormFacts)
		if err != nil {
			t.Fatal(err)
		}
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.FormContract, Input: json.RawMessage(`{"input_id":"ui-form"}`), Inputs: []rsi.InputData{{ID: contract.FormInput, Data: facts}}, Expected: c.Expected})
		formCases++
	}
	if formCases != 23 {
		t.Fatal("lost native answer cases", formCases)
	}
	planData, err := os.ReadFile("../plugin/intelligentui/testdata/original-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		Input    json.RawMessage `json:"input"`
		Expected struct {
			HTML string `json:"html"`
		} `json:"expected"`
	}
	if err = json.Unmarshal(planData, &plans); err != nil || len(plans) != 4 {
		t.Fatal("lost original Plan corpus", err)
	}
	css, err := os.ReadFile("../plugin/intelligentui/testdata/original-plan.css")
	if err != nil {
		t.Fatal(err)
	}
	// Owner wire equality uses the unambiguous original plain case.
	for _, plan := range plans[:1] {
		facts, _ := json.Marshal(contract.PlanFacts{Plans: []contract.PlanFact{{Component: 0, Props: plan.Input}}})
		expected, _ := json.Marshal(contract.PlanViewsResult{Views: []contract.PlanView{{Component: 0, HTML: plan.Expected.HTML, CSS: strings.Replace(string(css), "@media(max-width:760px){.native-plan summary{min-height:44px}}", "html[data-host-compact] .native-plan summary{min-height:44px}", 1), CompactMaxWidth: 760}}})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.PlanViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.PlanInput, Data: facts}}, Expected: expected})
	}
	cardsOracle, err := os.ReadFile("builtin/ui-cards-worker.json")
	if err != nil {
		t.Fatal(err)
	}
	var cardsCase struct {
		Facts    json.RawMessage `json:"facts"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(cardsOracle, &cardsCase); err != nil {
		t.Fatal(err)
	}
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.CardsViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.CardsInput, Data: cardsCase.Facts}}, Expected: cardsCase.Expected})

	// Reuse the independent pre-producer DOM and reviewed frozen Worker oracle,
	// never a value returned by the candidate currently under verification.
	tableOracle, err := os.ReadFile("builtin/ui-table-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var tableCase struct {
		Facts    json.RawMessage `json:"facts"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(tableOracle, &tableCase); err != nil {
		t.Fatal(err)
	}
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.TableViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.TableInput, Data: tableCase.Facts}}, Expected: tableCase.Expected})
	chartOracle, err := os.ReadFile("builtin/ui-charts-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var chartCases []struct {
		Facts    json.RawMessage `json:"facts"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(chartOracle, &chartCases); err != nil {
		t.Fatal(err)
	}
	if len(chartCases) != 4 {
		t.Fatal("independent chart owner cases missing")
	}
	for _, original := range chartCases {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.ChartViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.ChartInput, Data: original.Facts}}, Expected: original.Expected})
	}
	formOracle, err := os.ReadFile("builtin/ui-form-views-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var formViewCases []struct {
		Facts    json.RawMessage `json:"facts"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(formOracle, &formViewCases); err != nil {
		t.Fatal(err)
	}
	if len(formViewCases) != 4 {
		t.Fatal("independent form owner cases missing")
	}
	for _, original := range formViewCases {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.FormViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.FormViewInput, Data: original.Facts}}, Expected: original.Expected})
	}
	meetingSamples, err := os.ReadFile("builtin/ui-meeting-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var extraMeetings []struct {
		Contract string          `json:"contract"`
		Document string          `json:"document"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(meetingSamples, &extraMeetings); err != nil {
		t.Fatal(err)
	}
	for _, sample := range extraMeetings {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: sample.Contract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
	}
	actionSamples, err := os.ReadFile("builtin/ui-action-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var originalActions []struct {
		Document string          `json:"document"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(actionSamples, &originalActions); err != nil {
		t.Fatal(err)
	}
	for _, sample := range originalActions {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
	}
	if err = fixture.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.host.Propose(ctx, rsi.Candidate{ID: "ui-original-document", Baseline: status.Revision, Target: graph.Revision, PolicyID: policy.ID, Goal: "private UI document implementation parity"}); err != nil {
		t.Fatal(err)
	}
	decision, err := fixture.host.Verify(ctx, "ui-original-document")
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := fixture.host.Store.Decision(ctx, decision)
	if err != nil || !evidence.Passed || evidence.Cases != 58 {
		t.Fatal(evidence, err)
	}
	if _, err = fixture.host.Activate(ctx, rsi.Activation{OperationID: "activate-ui-document", Expected: status.Revision, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	// Independently captured DOM equality is checked after actual isolated RPCs.
	// Browser and Go may spell the same escaped text differently on the wire.
	canonical := func(source string) string {
		t.Helper()
		node, e := html.Parse(strings.NewReader(source))
		if e != nil {
			t.Fatal(e)
		}
		var out bytes.Buffer
		if e = html.Render(&out, node); e != nil {
			t.Fatal(e)
		}
		return out.String()
	}
	for _, plan := range plans {
		facts, _ := json.Marshal(contract.PlanFacts{Plans: []contract.PlanFact{{Component: 0, Props: plan.Input}}})
		raw, e := fixture.host.ReadService(ctx, graph.Revision, contract.PlanViews, json.RawMessage(`{}`), []rsi.InputData{{ID: contract.PlanInput, Data: facts}})
		var result contract.PlanViewsResult
		if e != nil || json.Unmarshal(raw, &result) != nil || result.ErrorCode != "" || len(result.Views) != 1 {
			t.Fatal("isolated Plan failed", string(raw), e)
		}
		view := result.Views[0]
		if canonical(view.HTML) != canonical(plan.Expected.HTML) || view.CSS != strings.Replace(string(css), "@media(max-width:760px){.native-plan summary{min-height:44px}}", "html[data-host-compact] .native-plan summary{min-height:44px}", 1) || view.CompactMaxWidth != 760 {
			t.Fatal("isolated original Plan changed", view)
		}
	}
	// Cards now ships in the same real default package. Keep its independent
	// original DOM oracle and descriptor negatives in this full-package gate.
	var cardsFacts contract.CardsFacts
	var wantCards contract.CardsViewsResult
	if json.Unmarshal(cardsCase.Facts, &cardsFacts) != nil || json.Unmarshal(cardsCase.Expected, &wantCards) != nil {
		t.Fatal("invalid frozen Cards oracle")
	}
	for _, ordinal := range []int{0, 1} {
		cardsFacts.Cards[0].Component = ordinal
		wantCards.Views[0].Component = ordinal
		facts, _ := json.Marshal(cardsFacts)
		raw, e := fixture.host.ReadService(ctx, graph.Revision, contract.CardsViews, json.RawMessage(`{}`), []rsi.InputData{{ID: contract.CardsInput, Data: facts}})
		var gotCards contract.CardsViewsResult
		if e != nil || json.Unmarshal(raw, &gotCards) != nil {
			t.Fatal("default isolated Cards failed", e)
		}
		actual, _ := json.Marshal(gotCards)
		expected, _ := json.Marshal(wantCards)
		if !bytes.Equal(actual, expected) {
			t.Fatal("default Cards changed original DOM/runtime/context", ordinal)
		}
	}
	for _, bad := range [][]rsi.InputData{nil, {{ID: "other-component", Data: cardsCase.Facts}}, {{ID: contract.CardsInput, Data: []byte(strings.Repeat(" ", (768<<10)+1))}}} {
		if _, e := fixture.host.ReadService(ctx, graph.Revision, contract.CardsViews, json.RawMessage(`{}`), bad); e == nil {
			t.Fatal("invalid Cards descriptor accepted")
		}
	}
	good := []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(cases[0].Input)}}
	if _, err = fixture.host.ReadService(ctx, graph.Revision, contract.Normalize, json.RawMessage(`{}`), good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]rsi.InputData{nil, {{ID: "other", Data: good[0].Data}}, {{ID: contract.DocumentInput, Data: []byte(strings.Repeat(" ", (128<<10)+1))}}} {
		if _, err = fixture.host.ReadService(ctx, graph.Revision, contract.Normalize, json.RawMessage(`{}`), bad); err == nil {
			t.Fatal("invalid document descriptor was accepted")
		}
	}
}
