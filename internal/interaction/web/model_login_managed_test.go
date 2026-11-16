package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"os"
	"testing"
)

type managedLoginProfiles struct {
	reconnectProfiles
	created int
}

func (p *managedLoginProfiles) ListProfiles(context.Context) ([]llm.Profile, error) { return nil, nil }
func (p *managedLoginProfiles) AddProfile(_ context.Context, input llm.ProfileInput) (llm.Profile, error) {
	p.created++
	p.profile = llm.Profile{ProfileID: "created", Version: 4, ProfileInput: input}
	return p.profile, nil
}
func TestModelConnectionLoginUsesOwnedChoiceAndHostAdmission(t *testing.T) {
	for _, mode := range []string{"owned", "historical", "error", "unknown", "empty", "catalog-changed", "pinned-other"} {
		t.Run(mode, func(t *testing.T) {
			profiles := &managedLoginProfiles{}
			core := &reconnectCore{status: Status{ConfigVersion: 11}}
			reads := 0
			calls := 0
			if mode == "pinned-other" {
				id := "other"
				core.status.ActiveModelProfileID = &id
				profiles.profile = llm.Profile{ProfileID: id, ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "different"}}
			}
			catalog := func(context.Context, string) ([]llm.CatalogModel, error) {
				reads++
				if mode == "catalog-changed" && reads > 1 {
					return nil, nil
				}
				return []llm.CatalogModel{{ModelID: "Zulu", ContextWindow: 8192, MaxOutputTokens: 1024}, {ModelID: "Alpha", ContextWindow: 8192, MaxOutputTokens: 1024}}, nil
			}
			s := Server{Core: core, Models: profiles, AccountCatalog: catalog, Control: llm.Control{Store: profiles, BridgeIdentity: "login-test", Probe: &struct{ llm.ModelProbe }{}, AccountCatalog: catalog, AccountAuth: func(context.Context, string) (int64, error) { return 7, nil }}}
			s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
				calls++
				if f.Login == nil || len(f.Login.Models) != 2 {
					t.Fatal(f)
				}
				out := contract.ConnectionDefaults{LoginModel: "Alpha"}
				switch mode {
				case "historical":
					return out, false, nil
				case "error":
					return out, true, errors.New("private failure")
				case "unknown":
					out.LoginModel = "foreign"
				case "empty":
					out.LoginModel = ""
				}
				return out, true, nil
			}
			e := s.selectAccountModelAfterLogin(context.Background(), "owned")
			if mode == "pinned-other" {
				if e != nil || calls != 0 || reads != 0 || profiles.created != 0 {
					t.Fatal("pinned choice was touched", e, calls, reads)
				}
				return
			}
			if calls != 1 {
				t.Fatal(calls)
			}
			if mode == "error" || mode == "unknown" || mode == "empty" || mode == "catalog-changed" {
				if e == nil || profiles.created != 0 || len(profiles.selected) != 0 || core.waits != 0 {
					t.Fatal("rejected owner caused effects or native fallback", e, profiles)
				}
				return
			}
			want := "Alpha"
			if mode == "historical" {
				want = "Zulu"
			}
			if e != nil || profiles.created != 1 || len(profiles.selected) != 1 || core.waits != 1 {
				t.Fatal(e, profiles.created, profiles.selected, core.waits)
			}
			selected := profiles.selected[0]
			if selected.Model.ModelID != want || selected.ConfigVersion != 11 || selected.AuthVersion != 7 || selected.ProfileVersion != 4 {
				t.Fatal("choice or frozen versions lost", selected)
			}
		})
	}
}
func TestModelConnectionLoginMatches5ExecutedNativeChoices(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/login-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Models   []string `json:"models"`
		Selected string   `json:"selected"`
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	if len(cases) != 5 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		out := owner.ProjectConnection(contract.ConnectionFacts{AccountBacked: true, Login: &contract.LoginFacts{Models: c.Models}})
		if out.LoginModel != c.Selected {
			t.Errorf("case %d got %q want %q", i, out.LoginModel, c.Selected)
		}
	}
}
