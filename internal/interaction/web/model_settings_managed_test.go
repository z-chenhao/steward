package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

func TestModelSettingsUsesOwnedProjectionAndBindsOriginalFacts(t *testing.T) {
	for _, scenario := range []string{"owned", "missing-copy", "missing-view", "error", "unknown", "missing", "foreign-group", "historical", "missing-target"} {
		t.Run(scenario, func(t *testing.T) {
			current := "original"
			s := Server{Models: pickerModels{profiles: []llm.Profile{{ProfileID: current, Version: 3, ProfileInput: llm.ProfileInput{ProviderKind: "openai_compatible", Name: "Original name", ModelID: "saved-model"}}}}}
			called := 0
			s.ModelSettings = func(_ context.Context, f contract.SettingsFacts) (contract.SettingsProjection, bool, error) {
				called++
				if len(f.Rows) != 1 || f.Rows[0].Ref != current || !f.Rows[0].Current || f.Rows[0].Verified {
					t.Fatal("authoritative source facts changed", f)
				}
				out := contract.SettingsProjection{View: owner.ProjectSettings(f).View, Copy: owner.ProjectSettings(f).Copy, Rows: []contract.SettingsChoice{{Ref: current, Open: true, Status: "Independent status"}}, SuppressNotice: true}
				switch scenario {
				case "missing-copy":
					out.Copy = nil
				case "missing-view":
					out.View = nil
				case "error":
					return out, true, errors.New("private candidate failure")
				case "unknown":
					out.Rows[0].Ref = "foreign"
				case "missing":
					out.Rows = nil
				case "foreign-group":
					out.Rows[0].Account = "foreign-account"
				case "historical":
					return contract.SettingsProjection{}, false, nil
				}
				return out, true, nil
			}
			target := current
			if scenario == "missing-target" {
				target = "absent"
			}
			r := httptest.NewRequest("GET", "http://localhost/settings/models?model="+target, nil)
			p := &page{Status: Status{ActiveModelProfileID: &current, ConfigVersion: 7}, Settings: &settingsView{}, Notice: "Original notice"}
			err := s.loadResourceModels(context.Background(), r, p)
			if scenario == "missing-target" {
				if !errors.Is(err, errPageNotFound) || called != 0 {
					t.Fatal("missing original target lost authority", called, err)
				}
				return
			}
			if called != 1 {
				t.Fatal("missing actual settings dispatch", called)
			}
			if scenario == "missing-copy" || scenario == "missing-view" || scenario == "error" || scenario == "unknown" || scenario == "missing" || scenario == "foreign-group" {
				if err == nil {
					t.Fatal("invalid current display restored native")
				}
				return
			}
			if err != nil || len(p.Settings.DirectModels) != 1 {
				t.Fatal(err, p.Settings)
			}
			row := p.Settings.DirectModels[0]
			want := "Independent status"
			if scenario == "historical" {
				want = "Needs attention"
			}
			if row.ConnectionStatus != want || row.ProfileID != current || row.Name != "Original name" || row.ModelID != "saved-model" || row.Version != 3 || row.ConfigVersion != 7 || !row.Current || row.Verified {
				t.Fatal("display replaced authoritative facts", row)
			}
			if scenario == "owned" && (!row.Open || p.Notice != "") {
				t.Fatal("owning display policy not consumed", row, p.Notice)
			}
		})
	}
}
