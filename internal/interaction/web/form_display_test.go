package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
)

type formDisplayCore struct {
	Core
	raw json.RawMessage
}

func (c formDisplayCore) WebProjection(context.Context, conversation.Message) (json.RawMessage, error) {
	return c.raw, nil
}

func TestPrivateFormDisplayRendersMainAndBubbleWithoutEnteringClientSpec(t *testing.T) {
	formID := strings.Repeat("a", 64)
	spec := ui.Spec{SchemaVersion: ui.Version, Components: []ui.Component{{Type: "openintelligentui", Name: "steward.form", FormID: formID, MessageID: "original-message", ViewRef: "ui:original#0", Props: json.RawMessage(`{"title":"Original","fields":[{"name":"confirm","label":"Confirm","type":"boolean"}]}`)}}}
	raw, err := json.Marshal(struct {
		ui.Spec
		Bubbles  []ui.Spec              `json:"bubbles"`
		Displays map[string]ui.FormView `json:"form_displays"`
	}{spec, []ui.Spec{spec}, map[string]ui.FormView{formID: {Props: ui.FormProps{Title: "Owned title", SubmitLabel: "Owned submit", Fields: []ui.FormField{{Name: "confirm", Label: "Owned confirm", Type: "boolean"}}}, Defaults: map[string]string{"confirm": "true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := Server{Core: formDisplayCore{raw: raw}}
	shown, err := server.showMessage(context.Background(), conversation.Message{}, map[string]interaction.Approval{})
	if err != nil {
		t.Fatal(err)
	}
	if len(shown.Bubbles) != 1 {
		t.Fatal("lost bubble")
	}
	for _, projection := range []shownProjection{shown.Projection, shown.Bubbles[0]} {
		for _, want := range []string{"Owned title", "Owned confirm", `value="true" selected`, `name="form_id" value="` + formID + `"`, `name="message_id" value="original-message"`} {
			if !strings.Contains(string(projection.HTML), want) {
				t.Fatalf("missing %s: %s", want, projection.HTML)
			}
		}
		for _, private := range []string{"form_displays", "Owned title", "Owned confirm", "Owned submit"} {
			if strings.Contains(projection.Spec, private) {
				t.Fatalf("private display leaked into client spec: %s", projection.Spec)
			}
		}
		var original ui.Spec
		if json.Unmarshal([]byte(projection.Spec), &original) != nil || original.Components[0].FormID != formID || original.Components[0].ViewRef != "ui:original#0" {
			t.Fatal("lost original identity")
		}
	}
}
