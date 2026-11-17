package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrozenFormViewDrivesNativeFallbackWithoutChangingFormIdentity(t *testing.T) {
	c := Component{Name: "steward.form", Type: "openintelligentui", FormID: strings.Repeat("a", 64), MessageID: "original-message", Props: json.RawMessage(`{"title":"Original","fields":[{"name":"confirm","label":"Confirm","type":"boolean"}]}`)}
	before, _ := json.Marshal(c)
	c.FormView = &FormView{Props: FormProps{Title: "Owned title", SubmitLabel: "Owned submit", Fields: []FormField{{Name: "confirm", Label: "Owned confirm", Type: "boolean"}}}, Defaults: map[string]string{"confirm": "true"}}
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		t.Fatal("private display changed frozen identity or entered the wire")
	}
	html := extensionFormHTML(c)
	for _, wanted := range []string{"Owned title", "Owned submit", "Owned confirm", `value="true" selected`, `name="form_id" value="` + c.FormID + `"`, `name="message_id" value="original-message"`} {
		if !strings.Contains(html, wanted) {
			t.Fatal("native fallback lost owning display or original identity", wanted, html)
		}
	}
	c.Response = `{"confirm":"false"}`
	c.Submitted = true
	if html = extensionFormHTML(c); !strings.Contains(html, `value="false" selected`) || strings.Contains(html, `value="true" selected`) {
		t.Fatal("owning initial value replaced accepted answer", html)
	}
}

func TestManagedFormFallbackNeverSubstitutesOriginalPropsForMissingDisplay(t *testing.T) {
	c := Component{Name: "steward.form", Type: "openintelligentui", Text: "Original fallback", ViewRef: "ui:original#1", Props: json.RawMessage(`{"title":"Native title","fields":[{"name":"notes","label":"Notes","type":"text"}]}`)}
	if html := extensionFormHTML(c); strings.Contains(html, "<form") || !strings.Contains(html, "Original fallback") {
		t.Fatal("missing managed display used native policy", html)
	}
	c.ViewRef = ""
	if html := extensionFormHTML(c); !strings.Contains(html, "<form") || !strings.Contains(html, "Native title") {
		t.Fatal("historical native form lost its original boundary", html)
	}
}
