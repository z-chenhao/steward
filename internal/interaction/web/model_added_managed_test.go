package web

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

type addActionProfiles struct {
	reconnectProfiles
	record                        llm.ProbeRecord
	hasProof                      bool
	selections                    int
	adds                          int
	selectedID                    string
	profileVersion, configVersion int64
}

func (p *addActionProfiles) AddProfile(_ context.Context, in llm.ProfileInput) (llm.Profile, error) {
	p.adds++
	p.profile = llm.Profile{ProfileID: "candidate", Version: 3, ProfileInput: in}
	return p.profile, nil
}
func (p *addActionProfiles) Probe(context.Context, string) (llm.ProbeRecord, error) {
	if !p.hasProof {
		return llm.ProbeRecord{}, llm.ErrNotFound
	}
	return p.record, nil
}
func (p *addActionProfiles) RecordProbe(_ context.Context, _ llm.Profile, r llm.ProbeRecord) error {
	p.hasProof = true
	p.record = r
	return nil
}
func (p *addActionProfiles) Select(_ context.Context, id string, pv, cv int64, _ string) error {
	p.selections++
	p.selectedID = id
	p.profileVersion = pv
	p.configVersion = cv
	return p.selectErr
}
func (p *addActionProfiles) InvalidateProbe(context.Context, string, int64, string) error { return nil }

type addActionProbe struct {
	calls int
	err   error
}

func (p *addActionProbe) Run(context.Context, llm.Profile) (llm.ProbeResult, error) {
	p.calls++
	if p.err != nil {
		return llm.ProbeResult{}, p.err
	}
	return llm.ProbeResult{Streaming: true, ToolCalls: true, Continuation: true, Cancellation: true, Clipping: true, ImageInput: true}, nil
}
func TestModelConnectionManagedAddedConnectionMatchesExecutedNativeActions(t *testing.T) {
	var cases []struct {
		HasCurrent    bool   `json:"has_current"`
		AccountBacked bool   `json:"account_backed"`
		Action        string `json:"action"`
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/add-action-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatal("missing native cases", len(cases))
	}
	for _, c := range cases {
		for _, mode := range []string{"owned", "independent-save", "forged-activate", "missing-action", "stopped", "stale-config", "probe-failure", "runtime-failure"} {
			t.Run(fmt.Sprintf("current=%t/account=%t/%s", c.HasCurrent, c.AccountBacked, mode), func(t *testing.T) {
				core := &reconnectCore{status: Status{ConfigVersion: 11}}
				other := "other"
				if c.HasCurrent {
					core.status.ActiveModelProfileID = &other
				}
				profiles := &addActionProfiles{}
				probe := &addActionProbe{}
				if mode == "stale-config" {
					profiles.selectErr = llm.ErrConflict
				}
				if mode == "probe-failure" {
					probe.err = llm.ProviderUnavailable
				}
				if mode == "runtime-failure" {
					core.waitErr = llm.ProviderUnavailable
				}
				catalog := func(context.Context, string) ([]llm.CatalogModel, error) {
					return []llm.CatalogModel{{ModelID: "original", ContextWindow: 8192, MaxOutputTokens: 1024}}, nil
				}
				s := &Server{Core: core, Models: profiles, AccountCatalog: catalog, Control: llm.Control{Store: profiles, Probe: probe, BridgeIdentity: "capture", AccountCatalog: catalog, AccountAuth: func(context.Context, string) (int64, error) { return 7, nil }}}
				calls := 0
				s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
					calls++
					if f.Added == nil || f.Added.HasCurrent != c.HasCurrent || f.AccountBacked != c.AccountBacked {
						t.Fatal("incorrect source facts", f)
					}
					out := owner.ProjectConnection(f)
					switch mode {
					case "independent-save":
						out.AddedAction = "save"
					case "forged-activate":
						out.AddedAction = "activate"
					case "missing-action":
						out.AddedAction = ""
					case "stopped":
						return out, true, llm.ErrNoBridge
					}
					return out, true, nil
				}
				form := url.Values{"model": {"original"}, "endpoint": {"http://127.0.0.1:8000/v1"}}
				if c.AccountBacked {
					form.Set("account", "owned")
				}
				r := httptest.NewRequest("POST", "/model/add", strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("Accept", "application/json")
				w := httptest.NewRecorder()
				err := s.post(w, r)
				denied := mode == "missing-action" || mode == "stopped" || mode == "forged-activate" && c.HasCurrent
				if denied {
					if err == nil || profiles.adds != 0 || profiles.selections != 0 || len(profiles.selected) != 0 || probe.calls != 0 || core.waits != 0 {
						t.Fatal("rejected policy wrote or activated", err, profiles, probe.calls, core.waits)
					}
					return
				}
				if calls != 1 || profiles.adds != 1 || err != nil {
					t.Fatal("policy re-read or candidate lost/duplicated", calls, profiles.adds, err)
				}
				action := c.Action
				if mode == "independent-save" {
					action = "save"
				}
				selections := profiles.selections + len(profiles.selected)
				failed := mode == "stale-config" && action == "activate" || mode == "probe-failure" && (action == "probe" || action == "activate" && !c.AccountBacked) || mode == "runtime-failure" && action == "activate"
				if failed {
					if w.Code != 409 {
						t.Fatal("post-save failure did not retain recovery handling", w.Code, w.Body.String())
					}
					if mode == "stale-config" && core.waits != 0 {
						t.Fatal("runtime admitted stale config")
					}
					return
				}
				if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "candidate") {
					t.Fatal("candidate resource lost", w.Code, w.Header())
				}
				switch action {
				case "activate":
					if selections != 1 || core.waits != 1 {
						t.Fatal("activation changed", selections, core.waits)
					}
					if c.AccountBacked {
						v := profiles.selected[0]
						if v.ProfileVersion != 3 || v.ConfigVersion != 11 || v.AuthVersion != 7 {
							t.Fatal("version fence changed", v)
						}
					} else if profiles.selectedID != "candidate" || profiles.profileVersion != 3 || profiles.configVersion != 11 {
						t.Fatal("API version fence changed", profiles)
					}
				case "probe":
					if selections != 0 || probe.calls != 1 || core.waits != 0 {
						t.Fatal("probe switched current", selections, probe.calls, core.waits)
					}
				case "save":
					if selections != 0 || probe.calls != 0 || core.waits != 0 {
						t.Fatal("save performed unsolicited effect", selections, probe.calls, core.waits)
					}
				}
			})
		}
	}
}
