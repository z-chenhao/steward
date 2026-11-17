package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// uiPreparation keeps business interpretation outside the writer transaction.
// Host retains invocation authority, privacy fences and publication quotas.
type uiPreparation struct {
	formViewManaged bool
	formViewOutput  json.RawMessage
	chartManaged    bool
	chartOutput     json.RawMessage
	managed         bool
	planManaged     bool
	planOutput      json.RawMessage
	cardsManaged    bool
	cardsOutput     json.RawMessage
	tableManaged    bool
	tableOutput     json.RawMessage
	args            string
	document        string
	failure         string
	access          evidenceAccess
}

func (t *tools) prepareUI(ctx context.Context, inv tool.Invocation) (p uiPreparation, err error) {
	p.managed, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.Normalize)
	if err != nil || !p.managed {
		return p, err
	}
	p.planManaged, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.PlanViews)
	if err != nil {
		return p, err
	}
	p.cardsManaged, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.CardsViews)
	if err != nil {
		return p, err
	}
	p.tableManaged, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.TableViews)
	if err != nil {
		return p, err
	}
	p.chartManaged, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.ChartViews)
	if err != nil {
		return p, err
	}
	p.formViewManaged, err = t.runs.managedRunService(ctx, t.claim.RunID(), contract.FormViews)
	if err != nil {
		return p, err
	}
	needed := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		kind, name, args, e := t.acceptedBuiltin(ctx, tx, inv)
		if e != nil {
			return e
		}
		if kind != "main" || name != ui.ShowName {
			return nil
		}
		p.args = args
		// Staging is already an accepted artifact. Recovery reuses its exact bytes.
		var saved string
		e = tx.QueryRowContext(ctx, `SELECT document_json FROM ui_widget WHERE invocation_id=? AND run_id=?`, inv.ID, t.claim.RunID()).Scan(&saved)
		if e == nil {
			p.document = saved
			if p.planManaged {
				var receipt string
				if e = tx.QueryRowContext(ctx, `SELECT p.result_json FROM ui_plan_view_snapshot p JOIN ui_widget w USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.invocation_id=? AND w.run_id=? AND p.graph_revision=b.graph_revision`, inv.ID, t.claim.RunID()).Scan(&receipt); e != nil {
					return e
				}
				var result contract.PlanViewsResult
				if e = rsiplugin.DecodeResult([]byte(receipt), &result); e != nil {
					return e
				}
				if e = validPlanViews(saved, result); e != nil {
					return e
				}
			}
			if p.cardsManaged {
				var receipt string
				if e = tx.QueryRowContext(ctx, `SELECT p.result_json FROM ui_cards_view_snapshot p JOIN ui_widget w USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.invocation_id=? AND w.run_id=? AND p.graph_revision=b.graph_revision`, inv.ID, t.claim.RunID()).Scan(&receipt); e != nil {
					return e
				}
				var result contract.CardsViewsResult
				if e = rsiplugin.DecodeResult([]byte(receipt), &result); e != nil {
					return e
				}
				if e = validCardsViews(saved, result); e != nil {
					return e
				}
			}
			if p.tableManaged {
				var receipt string
				if e = tx.QueryRowContext(ctx, `SELECT p.result_json FROM ui_table_view_snapshot p JOIN ui_widget w USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.invocation_id=? AND w.run_id=? AND p.graph_revision=b.graph_revision`, inv.ID, t.claim.RunID()).Scan(&receipt); e != nil {
					return e
				}
				var result contract.TableViewsResult
				if e = rsiplugin.DecodeResult([]byte(receipt), &result); e != nil {
					return e
				}
				if e = validTableViews(saved, result); e != nil {
					return e
				}
			}
			if p.chartManaged {
				var receipt string
				if e = tx.QueryRowContext(ctx, `SELECT p.result_json FROM ui_chart_view_snapshot p JOIN ui_widget w USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.invocation_id=? AND w.run_id=? AND p.graph_revision=b.graph_revision`, inv.ID, t.claim.RunID()).Scan(&receipt); e != nil {
					return e
				}
				var result contract.ChartViewsResult
				if e = rsiplugin.DecodeResult([]byte(receipt), &result); e != nil {
					return e
				}
				if e = validChartViews(saved, result); e != nil {
					return e
				}
			}
			if p.formViewManaged {
				var receipt string
				if e = tx.QueryRowContext(ctx, `SELECT p.result_json FROM ui_form_view_snapshot p JOIN ui_widget w USING(invocation_id) JOIN rsi_run b USING(run_id) WHERE p.invocation_id=? AND w.run_id=? AND p.graph_revision=b.graph_revision`, inv.ID, t.claim.RunID()).Scan(&receipt); e != nil {
					return e
				}
				var result contract.FormViewsResult
				if e = rsiplugin.DecodeResult([]byte(receipt), &result); e != nil {
					return e
				}
				if e = validFormViews(saved, result); e != nil {
					return e
				}
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		needed = true
		return nil
	})
	if err != nil || !needed {
		return p, err
	}
	h, a, err := t.pureToolPolicyHost(ctx, inv)
	if err != nil {
		return p, err
	}
	p.access = a
	b, err := h.ServiceInstance(ctx, a.pluginGraph, contract.Normalize)
	if err != nil {
		return p, err
	}
	d, err := h.ServiceInstance(ctx, a.pluginGraph, contract.DefinitionContract)
	if err != nil {
		return p, err
	}
	if b.InstanceID != d.InstanceID || b.PackageDigest != d.PackageDigest {
		return p, rsi.ErrInvalid
	}
	raw, err := h.CallWithInput(ctx, rsiplugin.Execution{RunID: t.claim.RunID()}, b.InstanceID, contract.Normalize, "ui-normalize:"+inv.ID, []byte(`{}`), []rsi.InputData{{ID: contract.DocumentInput, Data: []byte(p.args)}})
	if err != nil {
		return p, err
	}
	var result contract.Result
	if err = rsiplugin.DecodeResult(raw, &result); err != nil {
		return p, err
	}
	if result.ErrorCode != "" {
		if result.ErrorCode != "INVALID_ARGUMENT" || result.DocumentJSON != "" || result.Readable != "" {
			return p, rsi.ErrInvalid
		}
		p.failure = result.ErrorCode
		return p, nil
	}
	if !json.Valid([]byte(result.DocumentJSON)) {
		return p, rsi.ErrInvalid
	}
	pkg, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return p, err
	}
	acceptedDocument, _, err := storedUIAtVersion(result.DocumentJSON, true, string(raw), pkg.Manifest.Version)
	if err != nil {
		return p, rsi.ErrInvalid
	}
	// Persist resource identity alongside normalization before staging. Reads can
	// resolve original resources without executing a later plugin version.
	raw, err = h.Call(ctx, rsiplugin.Execution{RunID: t.claim.RunID()}, d.InstanceID, contract.DefinitionContract, "ui-definition:"+inv.ID, []byte(`{}`))
	if err != nil {
		return p, err
	}
	var definition contract.Definition
	if err = rsiplugin.DecodeResult(raw, &definition); err != nil {
		return p, err
	}
	if p.planManaged {
		viewBinding, e := h.ServiceInstance(ctx, a.pluginGraph, contract.PlanViews)
		if e != nil {
			return p, e
		}
		if viewBinding.InstanceID != b.InstanceID || viewBinding.PackageDigest != b.PackageDigest {
			return p, rsi.ErrInvalid
		}
		facts := contract.PlanFacts{Plans: []contract.PlanFact{}}
		for i, c := range acceptedDocument.Components {
			if c.Name == "plan_visualization" {
				facts.Plans = append(facts.Plans, contract.PlanFact{Component: i, Props: c.Props})
			}
		}
		input, e := json.Marshal(facts)
		if e != nil {
			return p, e
		}
		viewRaw, e := h.ReadService(ctx, a.pluginGraph, contract.PlanViews, []byte(`{}`), []rsi.InputData{{ID: contract.PlanInput, Data: input}})
		if e != nil {
			return p, e
		}
		var views contract.PlanViewsResult
		if e = rsiplugin.DecodeResult(viewRaw, &views); e != nil {
			return p, e
		}
		if e = validPlanViews(result.DocumentJSON, views); e != nil {
			return p, e
		}
		p.planOutput = viewRaw
	}
	if p.cardsManaged {
		viewBinding, e := h.ServiceInstance(ctx, a.pluginGraph, contract.CardsViews)
		if e != nil {
			return p, e
		}
		if viewBinding.InstanceID != b.InstanceID || viewBinding.PackageDigest != b.PackageDigest {
			return p, rsi.ErrInvalid
		}
		facts := contract.CardsFacts{Cards: []contract.CardsFact{}}
		for i, c := range acceptedDocument.Components {
			if c.Name == "steward.cards" {
				facts.Cards = append(facts.Cards, contract.CardsFact{Component: i, Props: c.Props})
			}
		}
		input, e := json.Marshal(facts)
		if e != nil {
			return p, e
		}
		viewRaw, e := h.ReadService(ctx, a.pluginGraph, contract.CardsViews, []byte(`{}`), []rsi.InputData{{ID: contract.CardsInput, Data: input}})
		if e != nil {
			return p, e
		}
		var views contract.CardsViewsResult
		if e = rsiplugin.DecodeResult(viewRaw, &views); e != nil {
			return p, e
		}
		if e = validCardsViews(result.DocumentJSON, views); e != nil {
			return p, e
		}
		p.cardsOutput = viewRaw
	}
	if p.tableManaged {
		viewBinding, e := h.ServiceInstance(ctx, a.pluginGraph, contract.TableViews)
		if e != nil {
			return p, e
		}
		if viewBinding.InstanceID != b.InstanceID || viewBinding.PackageDigest != b.PackageDigest {
			return p, rsi.ErrInvalid
		}
		facts := contract.TableFacts{Tables: []contract.TableFact{}}
		for i, c := range acceptedDocument.Components {
			if c.Name == "Table" {
				facts.Tables = append(facts.Tables, contract.TableFact{Component: i, Props: c.Props})
			}
		}
		input, e := json.Marshal(facts)
		if e != nil {
			return p, e
		}
		viewRaw, e := h.ReadService(ctx, a.pluginGraph, contract.TableViews, []byte(`{}`), []rsi.InputData{{ID: contract.TableInput, Data: input}})
		if e != nil {
			return p, e
		}
		var views contract.TableViewsResult
		if e = rsiplugin.DecodeResult(viewRaw, &views); e != nil {
			return p, e
		}
		if e = validTableViews(result.DocumentJSON, views); e != nil {
			return p, e
		}
		p.tableOutput = viewRaw
	}
	if p.chartManaged {
		viewBinding, e := h.ServiceInstance(ctx, a.pluginGraph, contract.ChartViews)
		if e != nil {
			return p, e
		}
		if viewBinding.InstanceID != b.InstanceID || viewBinding.PackageDigest != b.PackageDigest {
			return p, rsi.ErrInvalid
		}
		facts := contract.ChartFacts{Charts: []contract.ChartFact{}}
		for i, c := range acceptedDocument.Components {
			if isChartComponent(c.Name) {
				facts.Charts = append(facts.Charts, contract.ChartFact{Component: i, Name: c.Name, Props: c.Props})
			}
		}
		input, e := json.Marshal(facts)
		if e != nil {
			return p, e
		}
		viewRaw, e := h.ReadService(ctx, a.pluginGraph, contract.ChartViews, []byte(`{}`), []rsi.InputData{{ID: contract.ChartInput, Data: input}})
		if e != nil {
			return p, e
		}
		var views contract.ChartViewsResult
		if e = rsiplugin.DecodeResult(viewRaw, &views); e != nil {
			return p, e
		}
		if e = validChartViews(result.DocumentJSON, views); e != nil {
			return p, e
		}
		p.chartOutput = viewRaw
	}
	if p.formViewManaged {
		viewBinding, e := h.ServiceInstance(ctx, a.pluginGraph, contract.FormViews)
		if e != nil {
			return p, e
		}
		if viewBinding.InstanceID != b.InstanceID || viewBinding.PackageDigest != b.PackageDigest {
			return p, rsi.ErrInvalid
		}
		facts := contract.FormViewFacts{Forms: []contract.FormViewFact{}}
		for i, c := range acceptedDocument.Components {
			if c.Name == "steward.form" {
				facts.Forms = append(facts.Forms, contract.FormViewFact{Component: i, Name: c.Name, Props: c.Props})
			}
		}
		input, e := json.Marshal(facts)
		if e != nil {
			return p, e
		}
		viewRaw, e := h.ReadService(ctx, a.pluginGraph, contract.FormViews, []byte(`{}`), []rsi.InputData{{ID: contract.FormViewInput, Data: input}})
		if e != nil {
			return p, e
		}
		var views contract.FormViewsResult
		if e = rsiplugin.DecodeResult(viewRaw, &views); e != nil {
			return p, e
		}
		if e = validFormViews(result.DocumentJSON, views); e != nil {
			return p, e
		}
		p.formViewOutput = viewRaw
	}
	p.document = result.DocumentJSON
	return p, nil
}
