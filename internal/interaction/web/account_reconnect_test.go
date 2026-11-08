package web

import (
	"context"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
)

type reconnectCore struct {
	Core
	status  Status
	waits   int
	waitErr error
}

func (c *reconnectCore) WebStatus(context.Context) (Status, error) { return c.status, nil }
func (c *reconnectCore) WaitForRuntime(context.Context) error {
	c.waits++
	return c.waitErr
}

type reconnectProfiles struct {
	Models
	llm.ProfileStore
	profile    llm.Profile
	profileErr error
	selectErr  error
	selected   []llm.AccountSelection
}

func (p *reconnectProfiles) Profile(_ context.Context, id string) (llm.Profile, error) {
	if p.profileErr != nil {
		return llm.Profile{}, p.profileErr
	}
	if id != p.profile.ProfileID {
		return llm.Profile{}, llm.ErrNotFound
	}
	return p.profile, nil
}
func (p *reconnectProfiles) SelectAccount(_ context.Context, selection llm.AccountSelection) error {
	p.selected = append(p.selected, selection)
	return p.selectErr
}

func TestAccountReconnectRestoresOnlyItsCurrentModelThroughVersionedActivation(t *testing.T) {
	for _, mode := range []string{"restore", "other-account", "api-current", "missing-profile", "missing-model", "catalog-failure", "auth-loss", "config-switch", "profile-edit", "runtime-failure"} {
		t.Run(mode, func(t *testing.T) {
			active := "kept-profile"
			core := &reconnectCore{status: Status{Readiness: "degraded", ConfigVersion: 11, ActiveModelProfileID: &active}}
			profiles := &reconnectProfiles{profile: llm.Profile{ProfileID: active, Version: 3, Status: "reauth_required", ProfileInput: llm.ProfileInput{
				Name: "Kept model", ModelID: "kept-model", ProviderKind: "openai_account", WireAPI: "openai_responses", AuthConnectionID: "owned",
				ContextWindow: 8192, MaxOutputTokens: 1024, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true},
			}}}
			var wantErr error
			switch mode {
			case "other-account":
				profiles.profile.AuthConnectionID = "other"
			case "api-current":
				profiles.profile.ProviderKind, profiles.profile.Endpoint = "openai_compatible", "http://127.0.0.1:8000/v1"
			case "missing-profile":
				profiles.profileErr, wantErr = llm.ErrNotFound, llm.ErrNotFound
			case "missing-model":
				wantErr = llm.ModelNotFound
			case "catalog-failure", "runtime-failure":
				wantErr = llm.ProviderUnavailable
			case "auth-loss":
				wantErr = llm.AuthRequired
			case "config-switch", "profile-edit":
				profiles.selectErr, wantErr = llm.ErrConflict, llm.ErrConflict
			}
			if mode == "runtime-failure" {
				core.waitErr = wantErr
			}
			catalogReads := 0
			catalog := func(_ context.Context, account string) ([]llm.CatalogModel, error) {
				catalogReads++
				if account != "owned" {
					t.Fatal("read another account's catalog", account)
				}
				if mode == "catalog-failure" {
					return nil, llm.ProviderUnavailable
				}
				if mode == "config-switch" {
					core.status.ConfigVersion++
				}
				if mode == "profile-edit" {
					profiles.profile.Version++
				}
				models := []llm.CatalogModel{{ModelID: "new-default", ContextWindow: 8192, MaxOutputTokens: 1024}}
				if mode != "missing-model" {
					models = append(models, llm.CatalogModel{ModelID: "kept-model", ContextWindow: 8192, MaxOutputTokens: 1024})
				}
				return models, nil
			}
			s := &Server{Core: core, Models: profiles, AccountCatalog: catalog, Control: llm.Control{
				Store: profiles, BridgeIdentity: "reconnect-test", Probe: &struct{ llm.ModelProbe }{}, AccountCatalog: catalog,
				AccountAuth: func(context.Context, string) (int64, error) {
					if mode == "auth-loss" {
						return 0, llm.AuthRequired
					}
					return 7, nil
				},
			}}
			if err := s.selectAccountModelAfterLogin(context.Background(), "owned"); !errors.Is(err, wantErr) {
				t.Fatal(err, wantErr)
			}
			activated := mode == "restore" || mode == "config-switch" || mode == "profile-edit" || mode == "runtime-failure"
			if activated {
				if len(profiles.selected) != 1 {
					t.Fatal("activation was skipped or retried with newer versions", profiles.selected)
				}
				selection := profiles.selected[0]
				if selection.ProfileID != active || selection.ProfileVersion != 3 || selection.ConfigVersion != 11 || selection.AuthVersion != 7 || selection.Model.ModelID != "kept-model" {
					t.Fatal("reconnect replaced the choice or lost its version fence", selection)
				}
			} else if len(profiles.selected) != 0 {
				t.Fatal("unrelated or unavailable model was activated", profiles.selected)
			}
			wantWait := 0
			if mode == "restore" || mode == "runtime-failure" {
				wantWait = 1
			}
			if core.waits != wantWait {
				t.Fatal("runtime wait did not follow accepted activation", core.waits)
			}
			if (mode == "other-account" || mode == "api-current" || mode == "missing-profile" || mode == "auth-loss") && catalogReads != 0 {
				t.Fatal("reconnect read an unnecessary catalog", catalogReads)
			}
		})
	}
}
