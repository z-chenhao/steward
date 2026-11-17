package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func actualInterfaceProfile(t *testing.T) contract.Profile {
	return actualInterfaceProfileLocale(t, "en")
}
func actualInterfaceProfileLocale(t *testing.T, locale string) contract.Profile {
	t.Helper()
	var profile contract.BrowserProfile
	raw := actualAppsPresentationResult(t, contract.BrowserProfileInterface, json.RawMessage(`{"locale":"`+locale+`"}`))
	if err := rsiplugin.DecodeResult(raw, &profile); err != nil {
		t.Fatal(err)
	}
	return profile.Profile
}
func actualAppsPresentationResult(t *testing.T, service string, input json.RawMessage) json.RawMessage {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	carrier := filepath.Join(t.TempDir(), "steward")
	for _, target := range []struct{ out, path string }{{filepath.Join(dir, "plugin"), "../../../cmd/plugins/apps-interface"}, {carrier, "../../../cmd/steward"}} {
		if out, err := exec.Command("go", "build", "-o", target.out, target.path).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	raw, err := os.ReadFile("../../../plugins/apps-interface/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, resource := range manifest.Resources {
		raw, err := os.ReadFile(filepath.Join("../../../plugins/apps-interface", resource.Path))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, resource.Path)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	catalog := rsi.Catalog{Root: t.TempDir(), Launcher: carrier}
	pkg, err := catalog.Install(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	runner := rsi.Runner{Catalog: catalog}
	var result rsiplugin.Result
	if err := runner.Invoke(ctx, rsiplugin.Binding{InstanceID: "interface", PackageDigest: pkg.Digest}, "call", rsiplugin.Call{OperationID: "owner-read", Contract: service, Input: input, State: rsiplugin.State{Value: json.RawMessage(`{}`)}}, &result); err != nil {
		t.Fatal(err)
	}
	return result.Output
}

func TestAppsInterfaceManagedOriginal512TemplatesAnd32Errors(t *testing.T) {
	type scene struct {
		Kind, Language, Status, Selected  string
		Public, Confirmed                 bool
		LinkMode, CardFlags, ServiceFlags int
	}
	type rendered struct {
		SHA256                                                      string
		Bytes                                                       int
		Actions                                                     []string
		StatusLabel                                                 string
		Account, Warning, AuthLink, FirstVisible, AdditionalVisible bool
	}
	type example struct {
		Scene    scene
		Expected rendered
	}

	var fixtures struct {
		Templates []example
		Errors    []struct {
			Kind, Language string
			Wrapped        bool
			Expected       string
		}
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-interface-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Templates) != 512 || len(fixtures.Errors) != 32 {
		t.Fatal("original native fixture changed")
	}
	policies := actualLocalizedAppPolicies(t)
	for _, mode := range []string{"native", "managed"} {
		for i, f := range fixtures.Templates {
			t.Run(fmt.Sprintf("%s/%s/%s/%d", mode, f.Scene.Kind, f.Scene.Language, i), func(t *testing.T) {
				s := f.Scene
				connection := connectedapps.Connection{ID: "original_account", Toolkit: "sample_app", Name: "Original <App>", Alias: "工作 & 个人", AccountID: "private-account-id-not-for-render", Status: s.Status, Public: s.Public, RevocationConfirmed: s.Confirmed}
				view := &appsView{View: connectedapps.View{State: connectedapps.State{Revision: 7, UserID: "private-owner-not-for-render", KeyRef: "credential:reference_only", Connections: []connectedapps.Connection{connection}}}, ConnectionID: "original_account", ExpiresAt: 123456}
				p := page{Apps: view}
				if s.Selected != "all" {
					slug := "sample_app"
					if s.Selected == "other" {
						slug = "other_app"
					}
					view.Selected = &appCard{Toolkit: composio.Toolkit{Slug: slug, Name: "Original <App>"}}
				}
				if s.LinkMode != 0 {
					view.ConnectURL = "https://app.composio.dev/link/original"
					if s.LinkMode == 2 {
						view.ConnectionID = "other_account"
					}
				}
				name := "app-accounts"
				if s.Kind == "detail" {
					name = "app-detail"
					view.State.Connections = nil
					view.Suggested = s.CardFlags&1 != 0
					view.Selected = &appCard{Toolkit: composio.Toolkit{Slug: "sample_app", Name: "Original <App>", NoAuth: s.CardFlags&2 != 0}, Connected: s.CardFlags&4 != 0, PublicActive: s.CardFlags&8 != 0, Managed: s.CardFlags&16 != 0}
				}
				if s.Kind == "service" {
					name = "app-service-page"
					if s.ServiceFlags&1 != 0 {
						p.Notice = "Unsaved changes."
					}
					if s.ServiceFlags&2 != 0 {
						p.Error = "Original validation failure"
					}
					if s.ServiceFlags&4 != 0 {
						view.AuthConfigRows = []appAuthConfigRow{{App: "gmail", Config: "ac_original"}, {App: "raw <draft>", Config: "保留 & 原值"}}
					}
				}
				if mode == "managed" {
					managed := policies[s.Language]
					view.Interface = &managed
				}
				var out bytes.Buffer
				if err := requestLayout(languageRequest("GET", "/settings/apps", s.Language, "")).ExecuteTemplate(&out, name, p); err != nil {
					t.Fatal(err)
				}
				body := out.String()
				if mode == "managed" {
					ref := `<input type="hidden" name="interface_revision" value="fixture-original">`
					if strings.Count(body, `name="interface_revision"`) != strings.Count(body, ref) {
						t.Fatal("unproved interface reference")
					}
					body = strings.ReplaceAll(body, ref, "")
					if s.Kind == "detail" {
						attrs := fmt.Sprintf(` data-app-no-auth="%t" data-app-suggested="%t"`, s.CardFlags&2 != 0, s.CardFlags&1 != 0)
						if strings.Count(body, attrs) != 1 {
							t.Fatal("incorrect original card facts")
						}
						body = strings.Replace(body, attrs, "", 1)
					}
				}
				hash := sha256.Sum256([]byte(body))
				if len(body) != f.Expected.Bytes || hex.EncodeToString(hash[:]) != f.Expected.SHA256 {
					t.Fatalf("original rendered template changed kind=%s scene=%#v got=%s/%d want=%s/%d", s.Kind, s, hex.EncodeToString(hash[:]), len(body), f.Expected.SHA256, f.Expected.Bytes)
				}
			})
		}
	}
	causes := []error{connectedapps.ErrAuthConfigRequired, connectedapps.ErrConflict, connectedapps.ErrInvalid, connectedapps.ErrDenied, connectedapps.ErrAliasRequired, connectedapps.ErrLinkExpired, connectedapps.ErrUnavailable, errors.New("private SQL/cause must not appear")}
	for _, f := range fixtures.Errors {
		var i int
		if _, err := fmt.Sscan(f.Kind, &i); err != nil {
			t.Fatal(err)
		}
		cause := causes[i]
		if f.Wrapped {
			cause = fmt.Errorf("private wrapper: %w", cause)
		}
		for _, p := range []AppInterfacePolicy{NativeAppInterface(), policies[f.Language]} {
			got := p.profileText(f.Language, p.errorLabel(cause))
			if got != f.Expected {
				t.Fatalf("original private error changed %s != %s", got, f.Expected)
			}
		}
	}
}
