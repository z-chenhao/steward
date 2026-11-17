package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// UIPreview is an ephemeral read under the exact Run binding. It accepts both
// streamed prefixes and accepted staging documents, without durable calls or
// any writer transaction around candidate execution.
func (r *Runs) UIPreview(ctx context.Context, c agent.RunClaim, raw string) (ui.Preview, bool, error) {
	managed, err := r.managedRunService(ctx, c.RunID(), contract.PreviewContract)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if !managed {
		value, ok := ui.ParsePreview(raw)
		return value, ok, nil
	}
	if r.plugins == nil {
		return ui.Preview{}, false, rsi.ErrNotFound
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, c.RunID())
	if err != nil {
		return ui.Preview{}, false, err
	}
	var main bool
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT kind='main' FROM run WHERE run_id=?`, c.RunID()).Scan(&main)
	})
	if err != nil || !main {
		return ui.Preview{}, false, err
	}
	data, err := h.ReadService(ctx, revision, contract.PreviewContract, []byte(`{}`), []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(raw)}})
	if err != nil {
		return ui.Preview{}, false, err
	}
	var result contract.PreviewResult
	if err = rsiplugin.DecodeResult(data, &result); err != nil {
		return ui.Preview{}, false, err
	}
	if !result.Valid {
		if result.PreviewJSON != "" {
			return ui.Preview{}, false, rsi.ErrInvalid
		}
		return ui.Preview{}, false, nil
	}
	preview, err := inertUIPreview(result.PreviewJSON)
	if err != nil {
		return ui.Preview{}, false, err
	}
	previewBinding, err := h.ServiceInstance(ctx, revision, contract.PreviewContract)
	if err != nil {
		return ui.Preview{}, false, err
	}
	previewPackage, err := h.Catalog.Load(previewBinding.PackageDigest)
	if err != nil {
		return ui.Preview{}, false, err
	}
	meetingWidget := ui.Widget{Components: preview.Components}
	var meetingViews []contract.MeetingView
	for _, view := range preview.MeetingViews {
		meetingViews = append(meetingViews, view)
	}
	if err = bindAcceptedMeetingViews(&meetingWidget, meetingViews, previewPackage.Manifest.Version); err != nil {
		return ui.Preview{}, false, err
	}
	preview.MeetingManaged = contract.MeetingDisplayRequired(previewPackage.Manifest.Version)
	if !preview.MeetingManaged {
		preview.MeetingViews = nil
	}
	planManaged, err := r.managedRunService(ctx, c.RunID(), contract.PlanViews)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if planManaged {
		planBinding, e := h.ServiceInstance(ctx, revision, contract.PlanViews)
		if e != nil {
			return ui.Preview{}, false, e
		}
		previewBinding, e := h.ServiceInstance(ctx, revision, contract.PreviewContract)
		if e != nil {
			return ui.Preview{}, false, e
		}
		if planBinding.InstanceID != previewBinding.InstanceID || planBinding.PackageDigest != previewBinding.PackageDigest {
			return ui.Preview{}, false, rsi.ErrInvalid
		}
		facts := contract.PlanFacts{Plans: []contract.PlanFact{}}
		expected := map[int]bool{}
		for i, component := range preview.Components {
			if component.Name == "plan_visualization" {
				expected[i] = true
				facts.Plans = append(facts.Plans, contract.PlanFact{Component: i, Props: component.Props})
			}
		}
		preview.PlanManaged = true
		preview.PlanViews = map[int]ui.PlanView{}
		if len(facts.Plans) > 0 {
			input, e := json.Marshal(facts)
			if e != nil {
				return ui.Preview{}, false, e
			}
			data, e := h.ReadService(ctx, revision, contract.PlanViews, []byte(`{}`), []rsi.InputData{{ID: contract.PlanInput, Data: input}})
			if e != nil {
				return ui.Preview{}, false, e
			}
			var views contract.PlanViewsResult
			if rsiplugin.DecodeResult(data, &views) != nil || validPlanViewSlots(expected, views) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			for _, view := range views.Views {
				preview.PlanViews[view.Component] = ui.PlanView{HTML: view.HTML, CSS: view.CSS, CompactMaxWidth: view.CompactMaxWidth}
			}
		}
	}

	cardsManaged, err := r.managedRunService(ctx, c.RunID(), contract.CardsViews)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if cardsManaged {
		binding, e := h.ServiceInstance(ctx, revision, contract.CardsViews)
		if e != nil {
			return ui.Preview{}, false, e
		}
		owner, e := h.ServiceInstance(ctx, revision, contract.PreviewContract)
		if e != nil {
			return ui.Preview{}, false, e
		}
		if binding.InstanceID != owner.InstanceID || binding.PackageDigest != owner.PackageDigest {
			return ui.Preview{}, false, rsi.ErrInvalid
		}
		facts := contract.CardsFacts{Cards: []contract.CardsFact{}}
		expected := map[int]int{}
		for i, component := range preview.Components {
			if component.Name != "steward.cards" {
				continue
			}
			var props struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(component.Props, &props) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			expected[i] = len(props.Items)
			facts.Cards = append(facts.Cards, contract.CardsFact{Component: i, Props: component.Props})
		}
		preview.CardsManaged = true
		preview.CardsViews = map[int]ui.CardsView{}
		if len(facts.Cards) > 0 {
			input, e := json.Marshal(facts)
			if e != nil {
				return ui.Preview{}, false, e
			}
			data, e := h.ReadService(ctx, revision, contract.CardsViews, []byte(`{}`), []rsi.InputData{{ID: contract.CardsInput, Data: input}})
			if e != nil {
				return ui.Preview{}, false, e
			}
			var views contract.CardsViewsResult
			if rsiplugin.DecodeResult(data, &views) != nil || validCardsViewSlots(expected, views) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			for _, view := range views.Views {
				// Preview retains presentation only: no package script, image grant,
				// persistent model context or component identity is issued before delivery.
				preview.CardsViews[view.Component] = ui.CardsView{HTML: view.HTML, CSS: view.CSS, CompactMaxWidth: view.CompactMaxWidth}
			}
		}
	}

	tableManaged, err := r.managedRunService(ctx, c.RunID(), contract.TableViews)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if tableManaged {
		binding, e := h.ServiceInstance(ctx, revision, contract.TableViews)
		if e != nil {
			return ui.Preview{}, false, e
		}
		owner, e := h.ServiceInstance(ctx, revision, contract.PreviewContract)
		if e != nil {
			return ui.Preview{}, false, e
		}
		if binding.InstanceID != owner.InstanceID || binding.PackageDigest != owner.PackageDigest {
			return ui.Preview{}, false, rsi.ErrInvalid
		}
		facts := contract.TableFacts{Tables: []contract.TableFact{}}
		expected := map[int]int{}
		for i, component := range preview.Components {
			if component.Name != "Table" {
				continue
			}
			var props struct {
				Columns []string `json:"columns"`
			}
			if json.Unmarshal(component.Props, &props) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			expected[i] = len(props.Columns)
			facts.Tables = append(facts.Tables, contract.TableFact{Component: i, Props: component.Props})
		}
		preview.TableManaged = true
		preview.TableViews = map[int]ui.TableView{}
		if len(facts.Tables) > 0 {
			input, e := json.Marshal(facts)
			if e != nil {
				return ui.Preview{}, false, e
			}
			data, e := h.ReadService(ctx, revision, contract.TableViews, []byte(`{}`), []rsi.InputData{{ID: contract.TableInput, Data: input}})
			if e != nil {
				return ui.Preview{}, false, e
			}
			var views contract.TableViewsResult
			if rsiplugin.DecodeResult(data, &views) != nil || validTableViewSlots(expected, views) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			for _, view := range views.Views {
				// Private display only. No model contexts or published identity grants.
				// The route uses the fixed read-only driver and opaque Worker boundary.
				preview.TableViews[view.Component] = ui.TableView{HTML: view.HTML, CSS: view.CSS, Worker: view.Worker, CompactMaxWidth: view.CompactMaxWidth}
			}
		}
	}
	chartManaged, err := r.managedRunService(ctx, c.RunID(), contract.ChartViews)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if chartManaged {
		binding, e := h.ServiceInstance(ctx, revision, contract.ChartViews)
		if e != nil {
			return ui.Preview{}, false, e
		}
		owner, e := h.ServiceInstance(ctx, revision, contract.PreviewContract)
		if e != nil {
			return ui.Preview{}, false, e
		}
		if binding.InstanceID != owner.InstanceID || binding.PackageDigest != owner.PackageDigest {
			return ui.Preview{}, false, rsi.ErrInvalid
		}
		facts := contract.ChartFacts{Charts: []contract.ChartFact{}}
		for i, component := range preview.Components {
			if isChartComponent(component.Name) {
				facts.Charts = append(facts.Charts, contract.ChartFact{Component: i, Name: component.Name, Props: component.Props})
			}
		}
		preview.ChartManaged = true
		preview.ChartViews = map[int]ui.ChartView{}
		if len(facts.Charts) > 0 {
			input, e := json.Marshal(facts)
			if e != nil {
				return ui.Preview{}, false, e
			}
			data, e := h.ReadService(ctx, revision, contract.ChartViews, []byte(`{}`), []rsi.InputData{{ID: contract.ChartInput, Data: input}})
			if e != nil {
				return ui.Preview{}, false, e
			}
			document, e := json.Marshal(ui.Widget{Title: preview.Title, Fallback: preview.Title, Components: preview.Components})
			if e != nil {
				return ui.Preview{}, false, e
			}
			var views contract.ChartViewsResult
			if rsiplugin.DecodeResult(data, &views) != nil || validChartViews(string(document), views) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			for _, view := range views.Views {
				component := preview.Components[view.Component]
				preview.ChartViews[view.Component] = ui.ChartView{Worker: view.Worker, Title: view.Title, Description: view.Description, Unit: view.Unit, Name: component.Name, Props: component.Props}
			}
		}
	}
	formManaged, err := r.managedRunService(ctx, c.RunID(), contract.FormViews)
	if err != nil {
		return ui.Preview{}, false, err
	}
	if formManaged {
		binding, e := h.ServiceInstance(ctx, revision, contract.FormViews)
		if e != nil {
			return ui.Preview{}, false, e
		}
		for _, service := range []string{contract.PreviewContract, contract.FormContract} {
			owner, e := h.ServiceInstance(ctx, revision, service)
			if e != nil {
				return ui.Preview{}, false, e
			}
			if owner.InstanceID != binding.InstanceID || owner.PackageDigest != binding.PackageDigest {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
		}
		facts := contract.FormViewFacts{Forms: []contract.FormViewFact{}}
		for i, component := range preview.Components {
			if component.Name == "steward.form" {
				facts.Forms = append(facts.Forms, contract.FormViewFact{Component: i, Name: component.Name, Props: component.Props})
			}
		}
		preview.FormManaged = true
		preview.FormViews = map[int]ui.FormView{}
		if len(facts.Forms) > 0 {
			input, e := json.Marshal(facts)
			if e != nil {
				return ui.Preview{}, false, e
			}
			data, e := h.ReadService(ctx, revision, contract.FormViews, []byte(`{}`), []rsi.InputData{{ID: contract.FormViewInput, Data: input}})
			if e != nil {
				return ui.Preview{}, false, e
			}
			document, e := json.Marshal(ui.Widget{Title: preview.Title, Fallback: preview.Title, Components: preview.Components})
			if e != nil {
				return ui.Preview{}, false, e
			}
			var result contract.FormViewsResult
			if rsiplugin.DecodeResult(data, &result) != nil || validFormViews(string(document), result) != nil {
				return ui.Preview{}, false, rsi.ErrInvalid
			}
			for _, view := range result.Views {
				display := ui.FormView{Props: ui.FormProps{Title: view.Title, SubmitLabel: view.SubmitLabel, Fields: []ui.FormField{}}, Defaults: map[string]string{}}
				for _, field := range view.Fields {
					item := ui.FormField{Name: field.Name, Label: field.Label, Type: field.Type, Required: field.Required, Min: field.Min, Max: field.Max}
					for _, option := range field.Options {
						item.Options = append(item.Options, ui.FormOption{Value: option.Value, Label: option.Label})
					}
					display.Props.Fields = append(display.Props.Fields, item)
					display.Defaults[field.Name] = field.Default
				}
				// Static display data is safe on the existing authenticated preview wire.
				// It contains no code, published identity, ingress or state capability.
				preview.FormViews[view.Component] = display
			}
		}
	}
	// Recheck the original claim after candidate I/O. A graph check alone does
	// not prove the Run still owns the current privacy epoch.
	if err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error { return fence(ctx, tx, c) }); err != nil {
		return ui.Preview{}, false, err
	}
	if err = h.Store.CheckGraph(ctx, revision); err != nil {
		return ui.Preview{}, false, err
	}
	return preview, true, nil
}

// This is the existing inert transport boundary, not catalog/layout policy.
// Candidate data carries no Host identities or actions; unpublished artifacts
// and executable script fields cannot enter the temporary display.
func inertUIPreview(raw string) (ui.Preview, error) {
	var wire struct {
		MeetingViews []contract.MeetingView `json:"meeting_views,omitempty"`
		Title        string                 `json:"title"`
		Text         string                 `json:"text,omitempty"`
		Components   []struct {
			Name  string          `json:"name"`
			Props json.RawMessage `json:"props"`
		} `json:"components"`
	}
	if rsiplugin.DecodeResult([]byte(raw), &wire) != nil || utf8.RuneCountInString(wire.Title) > 200 || utf8.RuneCountInString(wire.Text) > 8192 || len(wire.Components) > 16 {
		return ui.Preview{}, rsi.ErrInvalid
	}
	out := ui.Preview{Title: wire.Title, Text: wire.Text}
	if len(wire.MeetingViews) > 0 {
		out.MeetingViews = map[int]contract.MeetingView{}
		for _, view := range wire.MeetingViews {
			if _, ok := out.MeetingViews[view.Component]; ok {
				return ui.Preview{}, rsi.ErrInvalid
			}
			out.MeetingViews[view.Component] = view
		}
	}
	for _, c := range wire.Components {
		if c.Name == "" || c.Name == "steward.action" {
			return ui.Preview{}, rsi.ErrInvalid
		}
		var props map[string]json.RawMessage
		if json.Unmarshal(c.Props, &props) != nil || props == nil {
			return ui.Preview{}, rsi.ErrInvalid
		}
		if c.Name == "generateSandboxedUi" {
			delete(props, "jsFunctions")
			delete(props, "jsExpressions")
		}
		if c.Name == "steward.cards" {
			var cards []map[string]json.RawMessage
			if json.Unmarshal(props["items"], &cards) == nil {
				for _, card := range cards {
					delete(card, "image_ref")
				}
				props["items"], _ = json.Marshal(cards)
			}
		}
		safe, _ := json.Marshal(props)
		out.Components = append(out.Components, ui.Component{Name: c.Name, Props: safe})
	}
	return out, nil
}

// UIPreviewBinding is an internal account-owned read for the current ephemeral
// session. It exposes no document, claim or model-supplied resource path.
func (s *Store) UIPreviewBinding(ctx context.Context, runID string) (string, string, error) {
	var revision, service string
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(b.graph_revision,''),CASE
 WHEN EXISTS(SELECT 1 FROM json_each(coalesce(b.managed_services_json,'[]')) x WHERE x.value='steward.ui-preview/1') THEN 'steward.ui-preview/1'
 WHEN EXISTS(SELECT 1 FROM json_each(coalesce(b.managed_services_json,'[]')) x WHERE x.value='steward.ui-document/1') THEN 'steward.ui-document/1' ELSE '' END
 FROM run r LEFT JOIN rsi_run b USING(run_id) WHERE r.run_id=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND r.desired_state='running' AND coalesce((SELECT epoch FROM run_memory_epoch WHERE run_id=r.run_id),0)=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=r.agent_id),0)`, runID).Scan(&revision, &service)
	if err != nil {
		return "", "", agent.ErrNotFound
	}
	return revision, service, nil
}
