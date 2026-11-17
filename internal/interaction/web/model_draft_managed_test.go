package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"net/http/httptest"
	"testing"
)

type draftSourceStore struct {
	llm.ProfileStore
	profile llm.Profile
}

func (s draftSourceStore) Profile(context.Context, string) (llm.Profile, error) {
	return s.profile, nil
}
func TestModelSettingsDraftBindsOriginalSourceAndRejectsFallback(t *testing.T) {
	for _, scenario := range []string{"owned", "missing-copy", "missing-view", "error", "missing", "forged-source", "invalid-source", "historical"} {
		t.Run(scenario, func(t *testing.T) {
			source := llm.Profile{ProfileInput: llm.ProfileInput{ProviderKind: "openai_compatible", Name: "Original", ModelID: "private-model", Endpoint: "http://localhost:8080/v1", WireAPI: "openai_responses"}}
			if scenario == "invalid-source" {
				source.ProviderKind = "openai_account"
			}
			s := Server{Control: llm.Control{Store: draftSourceStore{profile: source}}}
			calls := 0
			s.ModelSettings = func(_ context.Context, f contract.SettingsFacts) (contract.SettingsProjection, bool, error) {
				calls++
				if f.Draft == nil || f.Draft.OpenAIEndpoint || f.Draft.Method != "api" {
					t.Fatal("wrong private facts", f)
				}
				out := contract.SettingsProjection{View: owner.ProjectSettings(f).View, Copy: owner.ProjectSettings(f).Copy, Rows: []contract.SettingsChoice{}, Draft: &contract.DraftProjection{Method: "api", Endpoint: "https://candidate.invalid", Service: "independent", WireAPI: "owned", UseSource: true}}
				switch scenario {
				case "missing-copy":
					out.Copy = nil
				case "missing-view":
					out.View = nil
				case "error":
					return out, true, errors.New("private failure")
				case "missing":
					out.Draft = nil
				case "historical":
					return contract.SettingsProjection{}, false, nil
				}
				return out, true, nil
			}
			target := "http://localhost/settings/models/new?method=api&source=original"
			if scenario == "forged-source" {
				target = "http://localhost/settings/models/new?method=api"
			}
			p := &page{}
			err := s.loadSettings(context.Background(), httptest.NewRequest("GET", target, nil), p)
			if scenario == "missing-copy" || scenario == "missing-view" || scenario == "error" || scenario == "missing" || scenario == "forged-source" || scenario == "invalid-source" {
				if err == nil {
					t.Fatal("invalid owner restored default")
				}
				if scenario == "invalid-source" && calls != 0 {
					t.Fatal("unvalidated source dispatched")
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			v := p.Settings
			if v.Name != source.Name || v.Model != source.ModelID || v.Endpoint != source.Endpoint || v.WireAPI != source.WireAPI {
				t.Fatal("source values replaced", v)
			}
			want := "independent"
			if scenario == "historical" {
				want = "custom"
			}
			if v.Service != want {
				t.Fatal("owned policy not used", v)
			}
		})
	}
}
