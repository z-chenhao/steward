package web

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

func TestModelConnectionLoginRestoreMatchesNativeAndRetainsHostFences(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/login-restore-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Owned    bool `json:"owned"`
		Restored bool `json:"restored"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatal("native restore cases missing", len(cases))
	}
	for _, c := range cases {
		for _, mode := range []string{"owned", "independent-decline", "historical", "stopped", "missing", "foreign-choice", "profile-conflict", "config-conflict"} {
			t.Run(mode+map[bool]string{true: "/current-owned", false: "/other-account"}[c.Owned], func(t *testing.T) {
				current := "original"
				core := &reconnectCore{status: Status{ConfigVersion: 11, ActiveModelProfileID: &current}}
				account := "owned"
				if !c.Owned {
					account = "different"
				}
				profiles := &reconnectProfiles{profile: llm.Profile{ProfileID: current, Version: 3, ProfileInput: llm.ProfileInput{Name: "Original", ModelID: "original", ProviderKind: "openai_account", AuthConnectionID: account, WireAPI: "openai_responses", ContextWindow: 8192, MaxOutputTokens: 1024, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true}}}}
				reads := 0
				catalog := func(context.Context, string) ([]llm.CatalogModel, error) {
					reads++
					return []llm.CatalogModel{{ModelID: "new-default", ContextWindow: 8192, MaxOutputTokens: 1024}, {ModelID: "original", ContextWindow: 8192, MaxOutputTokens: 1024}}, nil
				}
				s := &Server{Core: core, Models: profiles, AccountCatalog: catalog, Control: llm.Control{Store: profiles, Probe: &struct{ llm.ModelProbe }{}, BridgeIdentity: "restore-test", AccountAuth: func(context.Context, string) (int64, error) { return 7, nil }, AccountCatalog: catalog}}
				calls := 0
				s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
					calls++
					if !f.AccountBacked || f.Login == nil || !f.Login.RestoreOwnedCurrent || len(f.Login.Models) != 0 || f.Service != "" || f.WireAPI != "" {
						t.Fatal("restore leaked or lost facts", f)
					}
					out := owner.ProjectConnection(f)
					switch mode {
					case "independent-decline", "historical":
						decline := false
						out.RestoreOwnedCurrent = &decline
					case "stopped":
						return out, true, llm.ErrNoBridge
					case "missing":
						out.RestoreOwnedCurrent = nil
					case "foreign-choice":
						out.LoginModel = "new-default"
					case "profile-conflict":
						profiles.profile.Version++
					case "config-conflict":
						profiles.selectErr = llm.ErrConflict
					}
					return out, mode != "historical", nil
				}
				err := s.selectAccountModelAfterLogin(context.Background(), "owned")
				if !c.Owned {
					if err != nil || calls != 0 || reads != 0 || len(profiles.selected) != 0 || core.waits != 0 {
						t.Fatal("other selection touched", err, calls, reads, profiles.selected, core.waits)
					}
					return
				}
				if calls != 1 {
					t.Fatal("restore bypassed owner", calls)
				}
				rejected := mode == "stopped" || mode == "missing" || mode == "foreign-choice" || mode == "profile-conflict"
				if rejected {
					if err == nil || len(profiles.selected) != 0 || reads != 0 || core.waits != 0 {
						t.Fatal("invalid policy restored native", err, profiles.selected, reads, core.waits)
					}
					return
				}
				if mode == "independent-decline" {
					if err != nil || len(profiles.selected) != 0 || reads != 0 || core.waits != 0 {
						t.Fatal("owner decline ignored", err, profiles.selected, reads, core.waits)
					}
					return
				}
				if len(profiles.selected) != 1 || reads != 1 {
					t.Fatal("original restore lost", err, profiles.selected, reads)
				}
				selection := profiles.selected[0]
				if selection.ProfileID != current || selection.ProfileVersion != 3 || selection.ConfigVersion != 11 || selection.AuthVersion != 7 || selection.Model.ModelID != "original" {
					t.Fatal("restore changed original choice/version", selection)
				}
				if mode == "config-conflict" {
					if err != llm.ErrConflict || core.waits != 0 {
						t.Fatal("runtime admitted rejected CAS", err, core.waits)
					}
				} else if err != nil || core.waits != 1 || !c.Restored {
					t.Fatal("native restore changed", err, core.waits, c.Restored)
				}
			})
		}
	}
}
