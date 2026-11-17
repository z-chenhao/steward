package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsAccountCopyActualSourcePreservesOriginalLocales(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		var actual contract.AccountComposition
		raw := actualAppsPresentationResult(t, contract.AccountCompositionInterface, json.RawMessage(`{"locale":"`+locale+`"}`))
		if err := rsiplugin.DecodeResult(raw, &actual); err != nil {
			t.Fatal(err)
		}
		if actual.AccountCopy != appAccountCopyLegacyV2(locale) {
			t.Fatalf("actual owning %s copy changed original display: %#v", locale, actual.AccountCopy)
		}
	}
}

func TestAppsAccountCopyFinalTextEscapesAndRetainsOriginalAuthority(t *testing.T) {
	load := func(name string, target any) {
		t.Helper()
		raw, err := os.ReadFile("../../../tests/fixtures/plugins/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	var c contract.AccountComposition
	load("apps-composition-order-native-v1.json", &c.Composition)
	c.AccountCopy = appAccountCopyLegacyV2("en")
	c.AccountCopy.Disconnect = "Disconnect" // An intentional English brand string must survive zh-CN.
	c.AccountCopy.ConfirmDisconnect = `Review "<script>private()</script>"`
	c.AccountCopy.OpenAuthorization = `Authorize <script>private()</script>`
	p, err := ManagedAppAccountComposition(AppInterfacePolicy{revision: "original_graph"}, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../plugins/apps-interface/views/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.AccountMarkupSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	p.accountMarkup, err = compileAppAccountMarkup(source.Account)
	if err != nil {
		t.Fatal(err)
	}
	p.accountManaged = true
	view := &appsView{Interface: &p, ConnectURL: "https://connect.composio.dev/original", ExpiresAt: 123456}
	row := appAccountContextView{appAccountView: appAccountView{Connection: connectedapps.Connection{ID: "original_account", Name: "Original app", Alias: "work", Status: "pending"}, UI: contract.Account{StatusLabel: "Pending", Warning: true, Pending: true, AuthorizationLink: true, Actions: []string{"disconnect"}}}, Apps: view}
	host := newLayout("zh-CN")
	actions, err := appAccountPart(host, row, "actions", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := appAccountPart(host, row, "copy", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(actions) + string(copy)
	for _, want := range []string{`>Disconnect</button>`, `name="connection" value="original_account"`, `value="original_graph"`, `href="https://connect.composio.dev/original"`, `value="disconnect"`, `Steward 已停止使用此账户。尚未确认外部服务已撤销授权；如有需要，请在该应用中检查已授权的服务。`, `&lt;script&gt;private()&lt;/script&gt;`} {
		if !strings.Contains(combined, want) {
			t.Fatal("owning final text or original authority lost", want, combined)
		}
	}
	if strings.Contains(combined, "<script>") || strings.Contains(combined, ">断开连接</button>") {
		t.Fatal("candidate final copy executed or was translated twice", combined)
	}
	for _, change := range []func(*contract.AccountCopy){func(c *contract.AccountCopy) { c.ConfirmDisconnect = "" }, func(c *contract.AccountCopy) { c.OpenAuthorization = strings.Repeat("x", 513) }} {
		invalid := c
		change(&invalid.AccountCopy)
		if _, err := ManagedAppAccountComposition(AppInterfacePolicy{revision: "original_graph"}, invalid); err == nil {
			t.Fatal("required bounded action copy became optional")
		}
	}
	unavailable := AppInterfacePolicy{}
	if result := appAccountCopy(&appsView{Interface: &unavailable}, "en"); result != (contract.AccountCopy{}) {
		t.Fatal("unavailable contribution restored legacy copy")
	}
}

func TestAppsAccountCopyCannotRewriteRequiredRevocationFacts(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-composition-order-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var c contract.AccountComposition
	if err := json.Unmarshal(raw, &c.Composition); err != nil {
		t.Fatal(err)
	}
	c.AccountCopy = appAccountCopyLegacyV2("en")
	raw, err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var labels map[string]string
	if err := json.Unmarshal(wire["account_copy"], &labels); err != nil {
		t.Fatal(err)
	}
	labels["revocation_warning"] = "All authorizations have been revoked."
	wire["account_copy"], err = json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var candidate contract.AccountComposition
	if err := rsiplugin.DecodeResult(raw, &candidate); err != nil {
		return
	}
	profileRaw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-interface-profile-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var profile contract.Profile
	if err := json.Unmarshal(profileRaw, &profile); err != nil {
		t.Fatal(err)
	}
	p, err := ManagedLocalizedAppInterface("original_graph", profile)
	if err != nil {
		t.Fatal(err)
	}
	p, err = ManagedAppAccountComposition(p, candidate)
	if err != nil {
		return
	}
	row := appAccountContextView{appAccountView: appAccountView{Connection: connectedapps.Connection{ID: "original_account", Toolkit: "gmail", Status: "disconnected", RevocationConfirmed: false}, UI: profile.Accounts[64]}, Apps: &appsView{Interface: &p}}
	if !row.UI.Warning {
		t.Fatal("original closed/unconfirmed Host facts lost required warning")
	}
	var result strings.Builder
	if err := newLayout("en").ExecuteTemplate(&result, "app-account-required-warning", row); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.String(), "Upstream revocation was not confirmed") {
		t.Fatal("candidate replaced required Host revocation facts", result.String())
	}
}
