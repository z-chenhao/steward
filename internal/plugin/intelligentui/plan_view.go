package intelligentui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Plan presentation is pure policy of the frozen UI package. Host owns source
// identity and publication; this producer receives only component positions and props.
type planView struct {
	HTML string
	CSS  string
}

//go:embed view/plan.css
var planCSS string

// Retained attribution: the original PlanCard structure follows the MIT
// OpenIntelligentUI adaptation documented in web/licenses/OpenIntelligentUI.txt.
var planTemplate = template.Must(template.New("plan").Parse(`<details class="native-plan"><summary><span>Plan: {{.Technology}}</span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 5 7 7-7 7"></path></svg></summary>{{if .Approach}}<div class="native-plan-body"><p>{{.Approach}}</p>{{if .KeyElements}}<ul>{{range .KeyElements}}<li>{{.}}</li>{{end}}</ul>{{end}}</div>{{end}}</details>`))

func renderPlanView(raw json.RawMessage) (planView, error) {
	c := Component{Name: "plan_visualization", Props: raw}
	var props nativeProps
	if !validNative(c) || json.Unmarshal(raw, &props) != nil {
		return planView{}, errors.New("invalid plan presentation")
	}
	if props.Technology == "" {
		props.Technology = "visualization"
	}
	var body bytes.Buffer
	if err := planTemplate.Execute(&body, props); err != nil {
		return planView{}, err
	}
	return planView{HTML: body.String(), CSS: planCSS}, nil
}

func planViews(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.PlanInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 768<<10 {
		return rsiplugin.Result{}, errors.New("invalid plan facts input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	out := contract.PlanViewsResult{Views: []contract.PlanView{}}
	var facts contract.PlanFacts
	if rsiplugin.DecodeInput(data, &facts) != nil || facts.Plans == nil || len(facts.Plans) > 16 {
		out.ErrorCode = "INVALID_ARGUMENT"
		return rsiplugin.JSONResult(ctx, out)
	}
	seen := map[int]bool{}
	for _, fact := range facts.Plans {
		if fact.Component < 0 || fact.Component > 15 || seen[fact.Component] {
			return rsiplugin.JSONResult(ctx, contract.PlanViewsResult{Views: []contract.PlanView{}, ErrorCode: "INVALID_ARGUMENT"})
		}
		seen[fact.Component] = true
		view, err := renderPlanView(fact.Props)
		if err != nil {
			return rsiplugin.JSONResult(ctx, contract.PlanViewsResult{Views: []contract.PlanView{}, ErrorCode: "INVALID_ARGUMENT"})
		}
		out.Views = append(out.Views, contract.PlanView{Component: fact.Component, HTML: view.HTML, CSS: view.CSS, CompactMaxWidth: 760})
	}
	return rsiplugin.JSONResult(ctx, out)
}
