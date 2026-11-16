package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/account"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"testing"
)

func TestComposerMenuUsesManagedProjectionAndRejectsInventedReferences(t *testing.T) {
	for _, scenario := range []string{"owned", "error", "unknown", "duplicate", "current", "historical"} {
		t.Run(scenario, func(t *testing.T) {
			s := Server{Core: settingsCore{}, Models: pickerModels{profiles: []llm.Profile{{ProfileID: "one", Version: 1, ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: "model", Name: "Private account label"}}}}, Accounts: &account.Manager{Store: pickerAccounts{}}}
			calls := 0
			s.ModelMenu = func(_ context.Context, f contract.Facts) (contract.Projection, bool, error) {
				calls++
				if len(f.Candidates) != 1 || f.Candidates[0].Name != "" {
					t.Fatal("unexpected facts or private account label", f)
				}
				c := contract.Choice{Key: f.Candidates[0].Key, Label: "Independent menu version"}
				out := contract.Projection{Choices: []contract.Choice{c}}
				switch scenario {
				case "error":
					return out, true, errors.New("owning plugin unavailable")
				case "unknown":
					out.Choices[0].Key = "account:foreign:invented"
				case "duplicate":
					out.Choices = append(out.Choices, c)
				case "current":
					out.Current = c.Key
				case "historical":
					return contract.Projection{}, false, nil
				}
				return out, true, nil
			}
			out, err := s.modelChoices(context.Background(), false)
			if calls != 1 {
				t.Fatal("missing real dispatch", calls)
			}
			if scenario == "error" || scenario == "unknown" || scenario == "duplicate" || scenario == "current" {
				if err == nil {
					t.Fatal("invalid managed menu silently accepted or replaced", out)
				}
				return
			}
			if err != nil || len(out.Choices) != 1 {
				t.Fatal(out, err)
			}
			want := "Independent menu version"
			if scenario == "historical" {
				want = "model"
			}
			if out.Choices[0].Label != want || out.Choices[0].Key != "account:owned:model" {
				t.Fatal("owning label or source identity lost", out)
			}
		})
	}
}
