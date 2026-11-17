package web

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func TestAppsInterfaceRejectsUnsafePlanWithoutNativeFallback(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-interface-profile-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*contract.Profile){
		"outside original scope":        func(p *contract.Profile) { p.Accounts[2].Visible = true },
		"hidden owner close":            func(p *contract.Profile) { p.Accounts[16].Actions = []string{"check"} },
		"hidden revocation uncertainty": func(p *contract.Profile) { p.Accounts[64].Warning = false },
		"unproved authorization link":   func(p *contract.Profile) { p.Accounts[0].AuthorizationLink = true },
		"invented active fact":          func(p *contract.Profile) { p.Browser.ActiveFold[0] = 2 },
		"unbounded automatic refresh":   func(p *contract.Profile) { p.Browser.PollIntervalMS = 1 },
		"missing cells":                 func(p *contract.Profile) { p.Cards = p.Cards[:63] },
		"duplicate action":              func(p *contract.Profile) { p.Accounts[0].Actions = []string{"check", "disconnect", "disconnect"} },
		"arbitrary action":              func(p *contract.Profile) { p.Accounts[0].Actions = []string{"execute"} },
		"missing private error label":   func(p *contract.Profile) { p.Errors[7] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			var p contract.Profile
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			mutate(&p)
			result, err := ManagedAppInterface("original", p)
			if err == nil || result.available() || result.native {
				t.Fatal("invalid plan admitted or recovered native")
			}
		})
	}
	var p contract.Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.Accounts[16].StatusLabel = `<script>unsafe()</script>`
	policy, err := ManagedAppInterface("owned", p)
	if err != nil {
		t.Fatal(err)
	}
	view := &appsView{Interface: &policy}
	view.State.Connections = []connectedapps.Connection{{ID: "original", Toolkit: "sample", Name: "Original <name>", Alias: "private & original", Status: "active"}}
	var rendered bytes.Buffer
	if err := layout.ExecuteTemplate(&rendered, "app-accounts", page{Apps: view}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.String(), "<script>unsafe") || !strings.Contains(rendered.String(), "&lt;script&gt;") || !strings.Contains(rendered.String(), "Original &lt;name&gt;") {
		t.Fatal("untrusted labels/source fields bypassed fixed escaping")
	}
}
