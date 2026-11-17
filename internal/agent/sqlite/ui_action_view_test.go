package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func originalActionView() contract.ActionView {
	return contract.ActionView{Component: 0, WorkingLabel: "Working", UnconfirmedError: "Action not confirmed. Retry.", UnknownError: "Result unknown. Check the conversation.", CancelledError: "Action cancelled.", NotExecutedError: "Action was not completed.", FailedError: "Action failed. Retry.", SuccessIndicator: true}
}
func TestUIActionViewBindsOriginalSlotAndKeepsAuthorityOut(t *testing.T) {
	makeWidget := func() ui.Widget {
		return ui.Widget{Components: []ui.Component{{Name: "steward.action", Props: json.RawMessage(`{"id":"refresh","label":"Refresh","tool":"mcp__ui__refresh","arguments":{}}`)}}}
	}
	view := originalActionView()
	view.UnknownError = "Review the original receipt."
	view.SuccessIndicator = false
	widget := makeWidget()
	before := ui.Hash(widget.Components[0].Props)
	if err := bindAcceptedActionViews(&widget, []contract.ActionView{view}, "1.27.0"); err != nil || !widget.Components[0].ActionManaged || widget.Components[0].ActionView.UnknownError != view.UnknownError || widget.Components[0].ActionView.SuccessIndicator {
		t.Fatal("owning feedback rejected", err)
	}
	if ui.Hash(widget.Components[0].Props) != before {
		t.Fatal("display changed request definition")
	}
	identity := ui.Hash(widget.Components)
	widget.Components[0].ActionView = nil
	if ui.Hash(widget.Components) != identity {
		t.Fatal("private display entered frozen component identity")
	}
	for _, version := range []string{"1.23.0", "1.25.0", "1.26.0"} {
		w := makeWidget()
		if err := bindAcceptedActionViews(&w, nil, version); err != nil || w.Components[0].ActionManaged {
			t.Fatal("historical view manufactured", version, err)
		}
	}
	for _, version := range []string{"1.27.0", "1.28.0", "2.0.0", "invalid"} {
		w := makeWidget()
		if bindAcceptedActionViews(&w, nil, version) == nil {
			t.Fatal("managed absence selected native policy", version)
		}
	}
	duplicate := makeWidget()
	duplicate.Components = append(duplicate.Components, duplicate.Components[0])
	if bindAcceptedActionViews(&duplicate, []contract.ActionView{originalActionView(), originalActionView()}, "1.27.0") == nil {
		t.Fatal("duplicate slot covered two original actions")
	}
	for name, mutate := range map[string]func(*contract.ActionView){"foreign slot": func(v *contract.ActionView) { v.Component = 1 }, "negative slot": func(v *contract.ActionView) { v.Component = -1 }, "empty message": func(v *contract.ActionView) { v.UnknownError = "" }, "private invalid bytes": func(v *contract.ActionView) { v.UnconfirmedError = "invalid\x00" }} {
		t.Run(name, func(t *testing.T) {
			w := makeWidget()
			bad := originalActionView()
			mutate(&bad)
			if bindAcceptedActionViews(&w, []contract.ActionView{bad}, "1.27.0") == nil {
				t.Fatal("invalid display accepted")
			}
		})
	}
}

func TestUIActionViewActualManagedCASFrozenRequestAndDeduplicatedReceipt(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, _, ref, counter := uiInteractionFixture(t, true)
	component, err := resolveUIComponent(ctx, store.db, ref+"#1")
	if err != nil || !component.ActionManaged || component.ActionView == nil || component.ActionView.WorkingLabel != "Working" {
		t.Fatal("real normalizer did not own action feedback", err)
	}
	saved, err := store.SaveWebUIState(ctx, ref+"#0", ui.ViewState{State: json.RawMessage(`{"filter":"A"}`), ModelContext: json.RawMessage(`{"selected":"A"}`)})
	if err != nil {
		t.Fatal(err)
	}
	source := "managed-action-original-source"
	first, err := runs.SubmitUIAction(ctx, ref+"#1", source)
	if err != nil {
		t.Fatal(err)
	}
	if counter.Load() != 0 {
		t.Fatal("view preparation executed action")
	}
	saved.State = json.RawMessage(`{"filter":"B"}`)
	if _, err = store.SaveWebUIState(ctx, ref+"#0", saved); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	result, err := store.WebUIActionResult(ctx, ref+"#1", source)
	if err != nil || result.Status != "succeeded" || counter.Load() != 1 {
		t.Fatal("original action did not use ordinary execution", result, err, counter.Load())
	}
	var arguments string
	if err = store.db.QueryRow(`SELECT arguments_json FROM ui_action_request WHERE source_id=?`, source).Scan(&arguments); err != nil || arguments != `{"filter":"A"}` {
		t.Fatal("display rebound request to changed state", arguments, err)
	}
	duplicate, err := runs.SubmitUIAction(ctx, ref+"#1", source)
	if err != nil || duplicate.MessageID != first.MessageID {
		t.Fatal("original source lost deduplication", duplicate, err)
	}
	mustDrain(t, actor)
	if counter.Load() != 1 {
		t.Fatal("display replayed original effect")
	}
	status, err := runs.plugins.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runs.plugins.Store.Deactivate(ctx, "action-view-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	runs.plugins = nil
	component, err = resolveUIComponent(ctx, store.db, ref+"#1")
	if err != nil || component.ActionView == nil || component.ActionView.UnknownError != "Result unknown. Check the conversation." {
		t.Fatal("deactivation lost frozen action view", err)
	}
	if err = store.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		return runs.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = resolveUIComponent(ctx, store.db, ref+"#1"); err == nil {
		t.Fatal("Forget retained private action view")
	}
}
