package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/account"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

type pickerModels struct {
	Models
	profiles []llm.Profile
}

func (m pickerModels) ListProfiles(context.Context) ([]llm.Profile, error) { return m.profiles, nil }

type pickerAccounts struct {
	account.Store
	entries []llm.AuthConnection
}

func (p pickerAccounts) Accounts(context.Context) ([]llm.AuthConnection, error) {
	if p.entries != nil {
		return p.entries, nil
	}
	return []llm.AuthConnection{{ConnectionID: "owned", Status: "connected"}, {ConnectionID: "signed-out", Status: "logged_out"}}, nil
}

func TestComposerCatalogIsReadOnlyAndFiltersStaleAccountModels(t *testing.T) {
	ctx := context.Background()
	model := func(id, name string) llm.Profile {
		return llm.Profile{ProfileID: id, ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: name}}
	}
	s := Server{Core: settingsCore{}, Models: pickerModels{profiles: []llm.Profile{model("one", "first"), model("stale", "retired")}}, Accounts: &account.Manager{Store: pickerAccounts{}}}
	reads := 0
	s.AccountCatalog = func(_ context.Context, id string) ([]llm.CatalogModel, error) {
		reads++
		if id != "owned" {
			t.Fatal("read disconnected account")
		}
		return []llm.CatalogModel{{ModelID: "first"}, {ModelID: "second"}}, nil
	}
	initial, err := s.modelChoices(ctx, false)
	if err != nil || reads != 0 || len(initial.Choices) != 2 {
		t.Fatal("page load fetched credentials", err, reads)
	}
	full, err := s.modelChoices(ctx, true)
	if err != nil || reads != 1 || len(full.Choices) != 2 || full.Choices[0].Key != "account:owned:second" {
		t.Fatal("available unsaved model missing or retired model retained", full, err)
	}
	// AddProfile is deliberately not implemented: a catalog GET must never write.
	s.AccountCatalog = func(context.Context, string) ([]llm.CatalogModel, error) {
		return nil, errors.New("private provider diagnostic")
	}
	fallback, err := s.modelChoices(ctx, true)
	if err != nil || !fallback.Unavailable || len(fallback.Choices) != 2 {
		t.Fatal("failed catalog lost local choices", fallback, err)
	}
}

func TestComposerModelsSortDescendingWithoutAccountNames(t *testing.T) {
	ctx := context.Background()
	profiles := []llm.Profile{
		{ProfileID: "lower", ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: "gpt-5.6-luna", Name: "gpt-5.6-luna · Private account"}},
		{ProfileID: "higher", ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: "gpt-6-luna"}},
	}
	accounts := pickerAccounts{entries: []llm.AuthConnection{
		{ConnectionID: "owned", Status: "connected", AuthInput: llm.AuthInput{AccountLabel: "Private account"}},
		{ConnectionID: "another", Status: "connected", AuthInput: llm.AuthInput{AccountLabel: "Another private account"}},
	}}
	s := Server{Core: settingsCore{}, Models: pickerModels{profiles: profiles}, Accounts: &account.Manager{Store: accounts}}
	reads := 0
	s.AccountCatalog = func(_ context.Context, id string) ([]llm.CatalogModel, error) {
		reads++
		if id == "another" {
			return []llm.CatalogModel{{ModelID: "gpt-6-luna"}}, nil
		}
		return []llm.CatalogModel{{ModelID: "gpt-5.6-luna"}, {ModelID: "gpt-6-luna"}}, nil
	}
	for _, catalog := range []bool{false, true} {
		got, err := s.modelChoices(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"gpt-6-luna", "gpt-5.6-luna"}
		if catalog {
			want = []string{"gpt-6-luna", "gpt-6-luna", "gpt-5.6-luna"}
		}
		if len(got.Choices) != len(want) {
			t.Fatal(got)
		}
		seen := map[string]bool{}
		for i, choice := range got.Choices {
			if choice.Label != want[i] || seen[choice.Key] {
				t.Fatal("name order, label or account routing changed", got)
			}
			seen[choice.Key] = true
		}
		if got.Choices[0].Key != "account:owned:gpt-6-luna" {
			t.Fatal("equal names lost stable account routing", got)
		}
		if !catalog && reads != 0 {
			t.Fatal("initial picker read account credentials")
		}
	}
}

// Use the existing required browser lane; ordinary control-plane tests stay fast.
func TestComposerModelsBrowserLoadingAndRefresh(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	s := languageServer()
	s.ModelMenu = func(_ context.Context, f contract.Facts) (contract.Projection, bool, error) {
		return owner.ProjectMenu(f), true, nil
	}
	s.Models = pickerModels{profiles: []llm.Profile{
		{ProfileID: "one", Version: 1, ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: "gpt-5.6-luna"}},
		{ProfileID: "two", Version: 1, ProfileInput: llm.ProfileInput{ProviderKind: "openai_account", AuthConnectionID: "owned", ModelID: "gpt-6-luna"}},
	}}
	s.Accounts = &account.Manager{Store: pickerAccounts{}}
	server := httptest.NewServer(s)
	defer server.Close()
	script, err := filepath.Abs("../../../tests/system/channels_browser.mjs")
	if err != nil {
		t.Fatal(err)
	}
	evidence := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", script, server.URL, filepath.Join(evidence, "composer-models"), "composer-models")
	cmd.Env = append(os.Environ(), "STEWARD_WEB_TEST_TOKEN=private-token-at-least-24-bytes")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser flow: %v\n%s", err, output)
	}
}
