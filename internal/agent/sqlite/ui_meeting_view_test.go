package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

const meetingDocument = `{"title":"Times","fallback":"Two actual options","components":[{"name":"scheduleTime","props":{"reasonForScheduling":"Review","meetingDuration":30,"timeSlots":[{"date":"2026-11-17","time":"09:15"},{"date":"2026-11-18","time":"14:00"}]}}]}`

func originalMeetingDisplay() contract.MeetingView {
	return contract.MeetingView{Component: 0, Title: "Review", Slots: []contract.MeetingSlot{{Choice: 0, Date: "2026-11-17", Time: "09:15", Duration: "30 min"}, {Choice: 1, Date: "2026-11-18", Time: "14:00", Duration: "30 min"}}, DeclineLabel: "None of these work", SelectedTitle: "Time Selected", DeclinedTitle: "No Time Selected", SavingLabel: "Saving selection…", SelectError: "Your selection was not confirmed. Retry the same time.", DeclineError: "Your selection was not confirmed. Retry the same choice.", SavedLabel: "Time preference saved."}
}
func TestUIMeetingViewBindsOriginalChoicesAndHistoricalVersion(t *testing.T) {
	check := func(views []contract.MeetingView, version string) (ui.Widget, error) {
		widget, err := managedUIDocument(meetingDocument)
		if err == nil {
			err = bindAcceptedMeetingViews(&widget, views, version)
		}
		return widget, err
	}
	view := originalMeetingDisplay()
	view.Title = "Owning Review"
	view.Slots[0], view.Slots[1] = view.Slots[1], view.Slots[0]
	widget, err := check([]contract.MeetingView{view}, "1.25.0")
	if err != nil || !widget.Components[0].MeetingManaged || widget.Components[0].MeetingView.Title != "Owning Review" || widget.Components[0].MeetingView.Slots[0].Choice != 1 {
		t.Fatal("independent display/order policy rejected", err)
	}
	before := ui.Hash(ui.ManagedWidgetComponents(widget, "ui:original", "message", 0))
	widget.Components[0].MeetingView = nil
	if before != ui.Hash(ui.ManagedWidgetComponents(widget, "ui:original", "message", 0)) {
		t.Fatal("private display changed original ingress identity")
	}
	for _, version := range []string{"1.0.0", "1.13.0", "1.23.0", "1.24.0"} {
		w, e := check(nil, version)
		if e != nil || w.Components[0].MeetingManaged {
			t.Fatal("historical version synthesized display", version, e)
		}
	}
	for _, version := range []string{"1.25.0", "1.26.0", "2.0.0", "invalid"} {
		if _, e := check(nil, version); e == nil {
			t.Fatal("new receipt absence fell back to old policy", version)
		}
	}
	for name, mutate := range map[string]func(*contract.MeetingView){"foreign component": func(v *contract.MeetingView) { v.Component = 1 }, "missing choice": func(v *contract.MeetingView) { v.Slots = v.Slots[:1] }, "duplicate choice": func(v *contract.MeetingView) { v.Slots[1] = v.Slots[0] }, "foreign date": func(v *contract.MeetingView) { v.Slots[0].Date = "tomorrow" }, "foreign time": func(v *contract.MeetingView) { v.Slots[0].Time = "00:00" }, "negative choice": func(v *contract.MeetingView) { v.Slots[0].Choice = -1 }, "missing label": func(v *contract.MeetingView) { v.DeclineLabel = "" }} {
		t.Run(name, func(t *testing.T) {
			bad := originalMeetingDisplay()
			mutate(&bad)
			if _, e := check([]contract.MeetingView{bad}, "1.25.0"); e == nil {
				t.Fatal("unbound display accepted")
			}
		})
	}
}

func TestUIMeetingViewRealReceiptRecoveryAndForget(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, provider := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtFormBoundary(t, store, runs, false, true)
	step := 0
	provider.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		step++
		if step == 1 {
			return calls(ui.ShowName, meetingDocument), nil
		}
		return llm.GenerationResult{Text: "Times ready."}, nil
	}
	mustSubmit(t, actor, "original-meeting", "Show real times")
	if _, err := store.db.Exec(`CREATE TRIGGER fail_meeting_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'meeting receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := actor.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("actual receipt fault not reached", err)
	}
	var ref, saved string
	if err := store.db.QueryRow(`SELECT w.ui_ref,c.result_json FROM ui_widget w JOIN rsi_call c ON c.operation_id='ui-normalize:'||w.invocation_id AND c.run_id=w.run_id`).Scan(&ref, &saved); err != nil {
		t.Fatal(err)
	}
	var accepted contract.Result
	if json.Unmarshal([]byte(saved), &accepted) != nil || len(accepted.MeetingViews) != 1 || accepted.MeetingViews[0].Slots[1].Choice != 1 {
		t.Fatal("actual normalizer did not produce owning view", saved)
	}
	runs.plugins = nil
	if _, err := store.db.Exec(`DROP TRIGGER fail_meeting_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, actor)
	var messageJSON string
	if err := store.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=?`, ref).Scan(&messageJSON); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err := json.Unmarshal([]byte(messageJSON), &message); err != nil {
		t.Fatal(err)
	}
	raw, err := store.WebProjection(ctx, message)
	if err != nil {
		t.Fatal("first recovered projection failed", err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	bubbles, err := store.WebBubbles(ctx, message, spec)
	if err != nil || len(bubbles) == 0 {
		t.Fatal("original bubble lineage lost", err)
	}
	found := false
	for _, c := range spec.Components {
		if c.Name == "scheduleTime" {
			found = true
			if !c.MeetingManaged || c.MeetingView == nil || c.MeetingView.Title != "Review" || c.FormID == "" || c.MessageID != message.MessageID {
				t.Fatal("first recovered read lost bound display", c)
			}
		}
	}
	if !found {
		t.Fatal("missing real meeting")
	}
	var after string
	if err = store.db.QueryRow(`SELECT result_json FROM rsi_call WHERE operation_id LIKE 'ui-normalize:%'`).Scan(&after); err != nil || after != saved {
		t.Fatal("recovery recomputed accepted display", err)
	}
	if scalar(t, store, `SELECT count(*) FROM rsi_call WHERE operation_id NOT LIKE 'ui-definition:%' AND operation_id NOT LIKE 'ui-normalize:%'`) != 0 {
		t.Fatal("display added durable operation")
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
	if _, err = store.WebProjection(ctx, message); err == nil {
		t.Fatal("Forget retained meeting projection")
	}
}

func TestUIMeetingViewPreviewUsesSamePureReceiptWithoutPublication(t *testing.T) {
	ctx := context.Background()
	store, runs, actor, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimeAtFormBoundary(t, store, runs, false, true)
	receipt := mustSubmit(t, actor, "meeting-preview", "Show actual times")
	claim, err := runs.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer runs.Release(ctx, claim)
	// Complete slots inside an incomplete outer tool call retain original ordinals.
	prefix := meetingDocument[:len(meetingDocument)-2]
	preview, valid, err := runs.UIPreview(ctx, claim, prefix)
	if err != nil || !valid || !preview.MeetingManaged || len(preview.MeetingViews) != 1 || preview.MeetingViews[0].Title != "Review" || preview.MeetingViews[0].Slots[1].Choice != 1 {
		t.Fatal("original pure preview display unavailable", valid, err, preview.MeetingViews)
	}
	wire, _ := json.Marshal(preview)
	for _, forbidden := range []string{`"form_id"`, `"message_id"`, `"widget_ref"`, `"view_ref"`, `"worker"`, `"script"`} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatal("preview acquired authority", forbidden)
		}
	}
	if scalar(t, store, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, store, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview acquired publication/durable operation")
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
	if _, _, err = runs.UIPreview(ctx, claim, prefix); err == nil {
		t.Fatal("Forget retained meeting preview")
	}
}
