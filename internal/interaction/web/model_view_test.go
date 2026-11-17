package web

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func modelViewFixture(mode string) page {
	v := &settingsView{Section: "models", Model: "saved-model", Endpoint: "http://localhost:8080/v1", Name: "Saved <model>", Service: "custom", WireAPI: "openai_responses"}
	p := page{Settings: v, AuthOnResources: true, Accounts: []llm.AuthConnection{{ConnectionID: "owned", AuthInput: llm.AuthInput{AccountLabel: "Original <account>"}, Status: "connected"}}}
	owned := owner.ProjectSettings(contract.SettingsFacts{WithView: true})
	v.View, v.Copy = owned.View, owned.Copy
	v.DirectModels = []modelResource{{Profile: llm.Profile{ProfileID: "original", Version: 3, ProfileInput: llm.ProfileInput{Name: "Original <name>", ModelID: "model<&>", Endpoint: "http://localhost:8080/v1"}}, ConfigVersion: 7, AuthVersion: 5, ConnectionStatus: "connected", Verified: true, KeyAuth: true}}
	switch mode {
	case "empty":
		v.DirectModels = nil
		p.Accounts = nil
	case "new-api":
		v.Section = "new"
	case "new-account":
		v.Section = "new"
		v.ConnectionMethod = "account"
	case "unverified":
		v.DirectModels[0].Verified = false
	case "disconnected":
		v.DirectModels[0].ConnectionStatus = "logged_out"
	case "pending":
		p.Auth.Status = "pending"
		p.Auth.Events = []llm.AuthEvent{{URL: "https://auth.openai.com/authorize?state=fixture", DeviceCode: "fixture-code"}}
	case "reauth":
		p.Auth.Status = "reauth_required"
	case "activation-error":
		p.Auth.Status = "connected"
		p.AuthError = "Signed in, but model activation failed. Choose a model in Chat."
	}
	return p
}
func TestModelSettingsViewMatchesOriginalNativeRendering(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/models.html")
	if e != nil {
		t.Fatal(e)
	}
	for _, language := range []string{"en", "zh-CN"} {
		for _, mode := range []string{"empty", "new-api", "new-account", "unverified", "disconnected", "pending", "reauth", "activation-error"} {
			p := modelViewFixture(mode)
			r := httptest.NewRequest("GET", "http://localhost/settings/models", nil)
			r.AddCookie(&http.Cookie{Name: "steward-language", Value: language})
			original, e := requestLayout(r).Parse(string(raw))
			if e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"model-list", "model-new", "signin"} {
				var want bytes.Buffer
				if e = original.ExecuteTemplate(&want, name, p); e != nil {
					t.Fatal(e)
				}
				got, e := renderModelView(p, r, name)
				if e != nil || stripModelBindingMetadata(string(got)) != want.String() {
					t.Fatalf("%s/%s/%s native mismatch err=%v\ngot=%s\nwant=%s", language, mode, name, e, got, want.String())
				}
			}
		}
	}
}
func TestModelSettingsViewRejectsForeignStateAndEffects(t *testing.T) {
	p := modelViewFixture("pending")
	r := httptest.NewRequest("GET", "http://localhost/settings/models", nil)
	original := p.Settings.View.Source
	for _, source := range []string{
		strings.Replace(original, " data-model-owner-connection", "", 1),
		strings.Replace(original, " data-model-owner-connection", " data-model-legacy-v1", 1),
		strings.Replace(original, `data-model-owner-key-required="true"`, "", 1),
		strings.Replace(original, `data-model-owner-endpoint-default="https://api.openai.com/v1"`, `data-model-owner-endpoint-default="javascript:alert(1)"`, 1),
		strings.Replace(original, "{{modelTr ", "{{tr ", 1),
		strings.Replace(original, "{{.Settings.Model}}", "{{.Settings.SecretRef}}", 1),
		strings.Replace(original, `{{define "model-new"}}`, `{{define "model-new"}}<script>alert(1)</script>`, 1),
		strings.Replace(original, `action="/model/add"`, `action="https://foreign.invalid/submit"`, 1),
		strings.Replace(original, `action="/model/add"`, `action="/api/auth"`, 1),
		strings.Replace(original, `name="api_key"`, `name="token"`, 1),
		strings.Replace(original, `data-api-connection`, `data-api-connection onclick="alert(1)"`, 1),
		strings.Replace(original, `data-api-connection`, `data-api-connection data-auto-submit`, 1),
		strings.Replace(original, `value="{{.Version}}"`, `value="999"`, -1),
		strings.Replace(original, `name="account" value="{{.ConnectionID}}"`, `name="account" value="foreign"`, -1),
		strings.Replace(original, `{{authURL .URL}}`, `https://foreign.invalid/collect`, 1),
	} {
		view := *p.Settings.View
		view.Source = source
		p.Settings.View = &view
		rejected := false
		for _, name := range []string{"model-list", "model-new"} {
			if _, e := renderModelView(p, r, name); e != nil {
				rejected = true
			}
		}
		if !rejected {
			t.Fatal("unsafe managed view accepted", source)
		}
	}
}

func TestModelSettingsViewPrivateHelpersCanChangeIndependently(t *testing.T) {
	p := modelViewFixture("unverified")
	r := httptest.NewRequest("GET", "http://localhost/settings/models", nil)
	original, e := renderModelView(p, r, "model-list")
	if e != nil {
		t.Fatal(e)
	}
	view := *p.Settings.View
	view.Source = strings.ReplaceAll(view.Source, `"model-resource"`, `"model-owner-row"`)
	p.Settings.View = &view
	got, e := renderModelView(p, r, "model-list")
	if e != nil || got != original {
		t.Fatal("private helper accidentally stabilized", e)
	}
}

func TestModelSettingsAuthenticationUpdatesStayInTheirOwningPage(t *testing.T) {
	s := &Server{Core: &updatesCore{}}
	s.auth = llm.AuthState{Status: "pending", Events: []llm.AuthEvent{{URL: "https://auth.openai.com/private-authorize", DeviceCode: "PRIVATE_FIXTURE_CODE"}}}
	s.authOnResources = true
	s.ModelSettings = func(context.Context, contract.SettingsFacts) (contract.SettingsProjection, bool, error) {
		t.Fatal("unrelated Settings invoked model plugin")
		return contract.SettingsProjection{}, false, nil
	}
	w := httptest.NewRecorder()
	s.updates(w, httptest.NewRequest("GET", "http://localhost/api/updates?view=/settings/preferences", nil))
	var out struct {
		Regions map[string]string `json:"regions"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Regions["signin-status"] != "" || strings.Contains(w.Body.String(), "PRIVATE_FIXTURE_CODE") {
		t.Fatal("foreign authentication region rendered", w.Code, w.Body.String())
	}
}

func TestModelSettingsCopyUsesOwnedDictionaryWithoutNativeFallback(t *testing.T) {
	p := modelViewFixture("empty")
	r := httptest.NewRequest("GET", "http://localhost/settings/models", nil)
	r.AddCookie(&http.Cookie{Name: languageCookie, Value: "zh-CN"})
	copy := *p.Settings.Copy
	copy.Source = `[{"key":"Models","value":"独立版本模型"}]`
	p.Settings.Copy = &copy
	got, e := renderModelView(p, r, "model-list")
	if e != nil || !strings.Contains(string(got), "<h1>独立版本模型</h1>") {
		t.Fatal("own dictionary ignored", e, got)
	}
	copy.Source = `[]`
	got, e = renderModelView(p, r, "model-list")
	if e != nil || !strings.Contains(string(got), "<h1>Models</h1>") {
		t.Fatal("unknown copy returned native translation", e, got)
	}
	copy.Source = `[{"key":"Models","value":"a"},{"key":"Models","value":"b"}]`
	if _, e = renderModelView(p, r, "model-list"); e == nil {
		t.Fatal("duplicate dictionary keys accepted")
	}
}

// Only the explicitly introduced nonvisual field-policy metadata is excluded
// from the frozen native markup comparison; UI content/controls remain exact.
func stripModelBindingMetadata(s string) string {
	return regexp.MustCompile(`\sdata-model-owner-(?:connection|key-required|endpoint-readonly|expand-advanced|endpoint-default|endpoint-force|endpoint-replace)(?:="[^"]*")?`).ReplaceAllString(s, "")
}
