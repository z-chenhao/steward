package sqlite

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func TestUIStoredManagedPreservesAcceptedReadableAndRejectsMissingReceipt(t *testing.T) {
	raw := `{"title":"Archived","fallback":"Original fallback","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	original, ok := ui.ParseWidget(raw)
	if !ok {
		t.Fatal("invalid native fixture")
	}
	document, _ := json.Marshal(original)
	receipt, _ := json.Marshal(contract.Result{DocumentJSON: string(document), Readable: "Frozen renderer wording: 42"})
	w, readable, err := storedUIAtVersion(string(document), true, string(receipt), "1.3.0")
	if err != nil || readable != "Frozen renderer wording: 42" || ui.Hash(ui.ManagedWidgetComponents(w, "ui:original", "reply", 0)) != ui.Hash(ui.WidgetComponents(original, "ui:original", "reply", 0)) {
		t.Fatal("accepted wording reinterpreted", w, readable, err)
	}
	_, native, err := storedUIAtVersion(string(document), false, "", "1.3.0")
	if err != nil || native != original.Readable() {
		t.Fatal("historical native boundary changed", native, err)
	}
	for _, bad := range []string{"", `{}`, `{"document_json":"null","readable":"forged","error_code":""}`} {
		if _, _, err := storedUIAtVersion(string(document), true, bad, "1.3.0"); err == nil {
			t.Fatal("missing or mismatched managed receipt restored native interpretation", bad)
		}
	}
	for _, legacy := range []ui.Widget{{Title: "Legacy", Fallback: "Legacy", HTML: "<script>legacy()</script>", Components: original.Components}, {Title: "Legacy", Fallback: "Legacy", JavaScript: "legacy()", Components: original.Components}, {Title: "Empty", Fallback: "Empty"}} {
		raw, _ := json.Marshal(legacy)
		saved, _ := json.Marshal(contract.Result{DocumentJSON: string(raw), Readable: "Legacy"})
		if _, _, err := storedUIAtVersion(string(raw), true, string(saved), "1.3.0"); err == nil {
			t.Fatal("managed receipt granted legacy executable or empty envelope")
		}
	}
	changed := original
	changed.Fallback = "Changed after acceptance"
	tampered, _ := json.Marshal(changed)
	if _, _, err := storedUIAtVersion(string(tampered), true, string(receipt), "1.3.0"); err == nil {
		t.Fatal("receipt accepted for a different document")
	}
}

func TestUIStoredManagedRejectsHostIdentityFields(t *testing.T) {
	base := `{"title":"Answer","fallback":"42","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	for _, field := range []string{`"approval_id":"approval:forged"`, `"message_id":"message:forged"`, `"form_id":"form:forged"`, `"submitted":true`, `"disabled":true`, `"type":"approval"`, `"widget_ref":"ui:other#0"`, `"artifact_ref":"artifact:other"`, `"frozen_arguments_hash":"forged"`, `"response":"accepted"`} {
		raw := strings.Replace(base, `"name":"Table"`, field+`,"name":"Table"`, 1)
		receipt, _ := json.Marshal(contract.Result{DocumentJSON: raw, Readable: "42"})
		if _, _, err := storedUIAtVersion(raw, true, string(receipt), "1.3.0"); err == nil {
			t.Fatal("plugin manufactured Host component field", field)
		}
	}
}

func TestUIStoredNewProjectionRequiresAcceptedComponentText(t *testing.T) {
	raw := `{"title":"Answer","fallback":"42","components":[{"name":"Table","props":{"title":"Value","columns":["N"],"rows":[["42"]],"source":"Calculated"}}]}`
	receipt, _ := json.Marshal(contract.Result{DocumentJSON: raw, Readable: "42", ComponentText: []string{"Independent versioned component summary"}})
	w, _, err := storedUIAtVersion(raw, true, string(receipt), "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	projected := ui.ManagedWidgetComponents(w, "ui:original", "reply", 0)
	if len(projected) != 1 || projected[0].Text != "Independent versioned component summary" {
		t.Fatal("Host recomputed component text", projected)
	}
	for _, version := range []string{"1.5.0", "2.0.0", "unknown"} {
		missing, _ := json.Marshal(contract.Result{DocumentJSON: raw, Readable: "42"})
		if _, _, err := storedUIAtVersion(raw, true, string(missing), version); err == nil {
			t.Fatal("new version restored native summary policy", version)
		}
	}
}
