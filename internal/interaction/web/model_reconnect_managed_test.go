package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"

	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	connectionv1 "github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

type nativeReconnectProbe struct{ calls int }

func (p *nativeReconnectProbe) Run(context.Context, llm.Profile) (llm.ProbeResult, error) {
	p.calls++
	return llm.ProbeResult{}, llm.ProviderUnavailable
}

type nativeReconnectProfiles struct{ reconnectProfiles }

func (p *nativeReconnectProfiles) InvalidateProbe(context.Context, string, int64, string) error {
	return nil
}
func TestModelConnectionManagedReconnectMatchesExecutedNativeActions(t *testing.T) {
	var cases []struct {
		Current string `json:"current"`
		Action  string `json:"action"`
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/reconnect-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 {
		t.Fatal("native cases missing", len(cases))
	}
	for _, c := range cases {
		for _, mode := range []string{"owned", "independent-probe", "forged-activate", "missing-action", "stopped", "stale-profile", "stale-config"} {
			t.Run(c.Current+"/"+mode, func(t *testing.T) {
				current := c.Current
				core := &reconnectCore{status: Status{ConfigVersion: 11}}
				if current != "" {
					core.status.ActiveModelProfileID = &current
				}
				profiles := &nativeReconnectProfiles{reconnectProfiles: reconnectProfiles{profile: llm.Profile{ProfileID: "target", Version: 3, ProfileInput: llm.ProfileInput{Name: "Original", ModelID: "original", ProviderKind: "openai_account", AuthConnectionID: "owned", WireAPI: "openai_responses", ContextWindow: 8192, MaxOutputTokens: 1024, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true}}}}}
				probe := &nativeReconnectProbe{}
				if mode == "stale-config" {
					profiles.selectErr = llm.ErrConflict
				}
				s := &Server{Core: core, Control: llm.Control{Store: profiles, Probe: probe, BridgeIdentity: "native-capture", AccountAuth: func(context.Context, string) (int64, error) { return 7, nil }, AccountCatalog: func(context.Context, string) ([]llm.CatalogModel, error) {
					return []llm.CatalogModel{{ModelID: "original", ContextWindow: 8192, MaxOutputTokens: 1024}}, nil
				}}}
				calls := 0
				s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
					calls++
					if f.Reconnect == nil || f.Reconnect.HasCurrent != (current != "") || f.Reconnect.TargetCurrent != (current == "target") {
						t.Fatal("incorrect authority-free facts", f)
					}
					out := connectionv1.Connection(f)
					switch mode {
					case "independent-probe":
						out.ReconnectAction = "probe"
					case "forged-activate":
						out.ReconnectAction = "activate"
					case "missing-action":
						out.ReconnectAction = ""
					case "stopped":
						return out, true, llm.ErrNoBridge
					}
					return out, true, nil
				}
				form := url.Values{"profile": {"target"}, "profile_version": {"3"}, "config_version": {"11"}}
				if mode == "stale-profile" {
					form.Set("profile_version", "2")
				}
				r := httptest.NewRequest("POST", "/model/reconnect", strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				err := s.reconnectModel(httptest.NewRecorder(), r)
				denied := mode == "missing-action" || mode == "stopped" || mode == "stale-profile" || mode == "forged-activate" && current == "other"
				if denied {
					if err == nil || len(profiles.selected) != 0 || probe.calls != 0 || core.waits != 0 {
						t.Fatal("rejected policy caused effects", err, profiles.selected, probe.calls, core.waits)
					}
					return
				}
				if calls != 1 {
					t.Fatal("owner not dispatched once", calls)
				}
				action := c.Action
				if mode == "independent-probe" {
					action = "probe"
				}
				if mode == "stale-config" && action == "activate" {
					if !errors.Is(err, llm.ErrConflict) || len(profiles.selected) != 1 || core.waits != 0 {
						t.Fatal("version fence lost", err, profiles.selected, core.waits)
					}
					return
				}
				if action == "activate" {
					if err != nil || len(profiles.selected) != 1 || probe.calls != 0 || core.waits != 1 {
						t.Fatal("activation changed", err, profiles.selected, probe.calls, core.waits)
					}
					selection := profiles.selected[0]
					if selection.ProfileID != "target" || selection.ProfileVersion != 3 || selection.ConfigVersion != 11 || selection.AuthVersion != 7 {
						t.Fatal("lost frozen references", selection)
					}
				} else if !errors.Is(err, llm.ProviderUnavailable) || len(profiles.selected) != 0 || probe.calls != 1 || core.waits != 0 {
					t.Fatal("probe changed", err, profiles.selected, probe.calls, core.waits)
				}
			})
		}
	}
}
