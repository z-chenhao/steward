package builtin

import (
	_ "embed"
	"encoding/json"
	"io"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Captured native tool declaration predates the candidate. The owning producer
// cannot supply its own schema oracle. Resource digests are computed directly
// from distribution files, independently of the definition service response.
//
//go:embed ui-definition.json
var originalUIDefinition []byte

// Original Chrome DOM/CSS is independent of the candidate. Only the explicit
// viewport selector adaptation differs; see intelligentui/testdata provenance.
//
//go:embed ui-plan-original.json
var originalUIPlan []byte

// Original DOM predates the producer; reviewed runtime/CSS bytes are frozen
// in this Owner oracle, never obtained from a candidate response.
//
//go:embed ui-cards-worker.json
var originalUICards []byte

// Table oracle uses pre-producer DOM plus reviewed, frozen Worker/CSS bytes.
//
//go:embed ui-table-original.json
var originalUITable []byte

// Reviewed private chart-family output plus fixed Worker baseline.
//
//go:embed ui-charts-original.json
var originalUICharts []byte

//go:embed ui-form-views-original.json
var originalUIFormViews []byte

// Static original control facts and text, independent of candidate output.
//
//go:embed ui-meeting-original.json
var originalUIMeeting []byte

// Original action feedback is captured independently of the candidate.
//
//go:embed ui-action-original.json
var originalUIAction []byte

func uiCases(directory, version string, planServiceDeclared, cardsServiceDeclared, tableServiceDeclared, chartServiceDeclared, formServiceDeclared bool) ([]rsi.Case, error) {
	var definition contract.Definition
	if err := json.Unmarshal(originalUIDefinition, &definition); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	digest := func(path string) (string, error) {
		f, err := root.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
		if err != nil {
			return "", err
		}
		if len(raw) == 0 || len(raw) > 32<<20 {
			return "", rsi.ErrInvalid
		}
		return rsiplugin.FileDigest(raw), nil
	}
	runtime, err := digest(contract.RuntimeFile)
	if err != nil {
		return nil, err
	}
	styles, err := digest(contract.StylesFile)
	if err != nil {
		return nil, err
	}
	definition.Runtime = contract.ViewResource{Name: contract.RuntimeResource, Digest: runtime}
	definition.Styles = contract.ViewResource{Name: contract.StylesResource, Digest: styles}
	encodedDefinition, _ := json.Marshal(definition)
	document := `{"title":"Fixture","fallback":"Fixture","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	normalized, _ := json.Marshal(contract.Result{DocumentJSON: document, Readable: "Fixture", ComponentText: []string{"Value\nN\n42\nCalculated"}})
	rejected, _ := json.Marshal(contract.Result{ErrorCode: "INVALID_ARGUMENT"})
	rawPreview := `{"title":"Preview","components":[{"name":"generateSandboxedUi","props":{"initialHeight":240,"html":"<p>42","jsFunctions":"PRIVATE_CODE","jsExpressions":["PRIVATE_CODE()"]}}]}`
	expectedPreview, _ := json.Marshal(contract.PreviewResult{Valid: true, PreviewJSON: `{"title":"Preview","components":[{"name":"generateSandboxedUi","props":{"css":"","cssComplete":false,"html":"\u003cp\u003e42","initialHeight":240}}]}`})
	cases := []rsi.Case{
		{InstanceID: "intelligent-ui", Contract: contract.FormContract, Input: json.RawMessage(`{"input_id":"ui-form"}`), Inputs: []rsi.InputData{{ID: contract.FormInput, Data: []byte(`{"name":"scheduleTime","props":{"reasonForScheduling":"Interview","meetingDuration":45,"timeSlots":[{"date":"2026-11-16","time":"10:00"},{"date":"2026-11-17","time":"11:00"}]},"values":{"choice":"0"}}`)}}, Expected: json.RawMessage(`{"parts":["Meeting time preference for Interview: 2026-11-16 at 10:00 (45 min). This selects a preferred time; it does not book a meeting."],"response":"0","error_code":""}`)},
		{InstanceID: "intelligent-ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(document)}}, Expected: normalized},
		{InstanceID: "intelligent-ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(`{}`)}}, Expected: rejected},
		{InstanceID: "intelligent-ui", Contract: contract.PreviewContract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(rawPreview)}}, Expected: expectedPreview},
		{InstanceID: "intelligent-ui", Contract: contract.DefinitionContract, Input: json.RawMessage(`{}`), Expected: encodedDefinition},
	}
	if contract.MeetingDisplayRequired(version) {
		var samples []struct {
			Contract string          `json:"contract"`
			Document string          `json:"document"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUIMeeting, &samples); err != nil {
			return nil, err
		}
		for _, sample := range samples {
			cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: sample.Contract, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
		}
	}
	if contract.ActionDisplayRequired(version) {
		var samples []struct {
			Document string          `json:"document"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUIAction, &samples); err != nil {
			return nil, err
		}
		for _, sample := range samples {
			cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.Normalize, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(sample.Document)}}, Expected: sample.Expected})
		}
	}
	if planServiceDeclared {
		cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.PlanViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.PlanInput, Data: []byte(`{"plans":[{"component":0,"props":{"approach":"Ship core","technology":"Go","key_elements":["Loop","Host"]}}]}`)}}, Expected: json.RawMessage(originalUIPlan)})
	}
	if cardsServiceDeclared {
		var oracle struct {
			Facts    json.RawMessage `json:"facts"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUICards, &oracle); err != nil {
			return nil, err
		}
		cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.CardsViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.CardsInput, Data: oracle.Facts}}, Expected: oracle.Expected})
	}

	if tableServiceDeclared {
		var oracle struct {
			Facts    json.RawMessage `json:"facts"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUITable, &oracle); err != nil {
			return nil, err
		}
		cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.TableViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.TableInput, Data: oracle.Facts}}, Expected: oracle.Expected})
	}
	if chartServiceDeclared {
		var samples []struct {
			Facts    json.RawMessage `json:"facts"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUICharts, &samples); err != nil {
			return nil, err
		}
		for _, sample := range samples {
			cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.ChartViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.ChartInput, Data: sample.Facts}}, Expected: sample.Expected})
		}
	}
	if formServiceDeclared {
		var samples []struct {
			Facts    json.RawMessage `json:"facts"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(originalUIFormViews, &samples); err != nil {
			return nil, err
		}
		for _, sample := range samples {
			cases = append(cases, rsi.Case{InstanceID: "intelligent-ui", Contract: contract.FormViews, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: contract.FormViewInput, Data: sample.Facts}}, Expected: sample.Expected})
		}
	}
	return cases, nil
}
