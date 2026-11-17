package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures(t *testing.T) {
	type scenario struct {
		Name       string           `json:"name"`
		Slug       string           `json:"slug"`
		Configured bool             `json:"configured"`
		Connected  bool             `json:"connected"`
		Mode       string           `json:"mode"`
		Expected   composio.Toolkit `json:"expected"`
		Error      string           `json:"error"`
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-directory-detail-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []scenario
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	if err := secrets.Put(ctx, "credential:detail", []byte("synthetic-detail-project-secret")); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	a.connectedApps.ReadEditorial = func(c context.Context, q contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
		return a.readAppsEditorialWithHost(c, host, q)
	}
	activateEditorialVersion(t, ctx, host, "", "1.0.0")
	captionRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-card-captions-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var captionCases []struct{ Name, Editorial, Provider, Expected string }
	if err := json.Unmarshal(captionRaw, &captionCases); err != nil {
		t.Fatal(err)
	}
	captionSelection, err := a.connectedApps.Directory(ctx, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range captionCases {
		t.Run("caption-"+c.Name, func(t *testing.T) {
			output, err := captionSelection.ProjectCaptions(ctx, []connectedapps.CaptionSources{{Editorial: c.Editorial, Provider: c.Provider}})
			if err != nil || len(output) != 1 || output[0] != c.Expected {
				t.Fatal("original native caption changed", output, c.Expected, err)
			}
		})
	}

	labelRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-card-labels-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var labelCases []struct{ Slug, Name, ExpectedName, ExpectedInitial string }
	if err := json.Unmarshal(labelRaw, &labelCases); err != nil {
		t.Fatal(err)
	}
	for _, c := range labelCases {
		t.Run("label-"+c.Slug, func(t *testing.T) {
			label, err := captionSelection.Label(c.Name, c.Slug)
			if err != nil || label.Name != c.ExpectedName || label.Initial != c.ExpectedInitial {
				t.Fatal("original native label changed", label, c, err)
			}
		})
	}
	var mu sync.Mutex
	var current scenario
	var optionFacts []composio.Category
	optionSelected := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		c := current
		facts, selected := optionFacts, optionSelected
		mu.Unlock()
		if r.URL.Path == "/toolkits" {
			if r.Method != "GET" || r.Header.Get("x-api-key") != "synthetic-detail-project-secret" || r.URL.Query().Get("category") != selected {
				t.Error("category query authority changed")
				http.Error(w, "invalid", 400)
				return
			}
			item := composio.Toolkit{Slug: "example", Name: "Example"}
			item.Meta.Categories = facts
			_ = json.NewEncoder(w).Encode(composio.Catalog{Items: []composio.Toolkit{item}})
			return
		}
		if r.Method != "GET" || r.URL.Path != "/toolkits/"+c.Slug || r.Header.Get("x-api-key") != "synthetic-detail-project-secret" {
			t.Error("original detail source changed", r.URL)
			http.Error(w, "invalid", 400)
			return
		}
		if c.Mode == "error" {
			http.Error(w, "unavailable", 500)
			return
		}
		slug := c.Slug
		if c.Mode == "mismatch" {
			slug = "different"
		}
		_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: slug, Name: "Live " + c.Slug, NoAuth: true})
	}))
	defer peer.Close()
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// Existing synthetic facts are fixture inputs, not an account-creation or
			// recovery test. The expected outputs were captured before this migration.
			state, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state.KeyRef = ""
			state.Connections = nil
			if c.Configured {
				state.KeyRef = "credential:detail"
			}
			if c.Connected {
				state.Connections = []connectedapps.Connection{{ID: "saved", Toolkit: c.Slug, Name: "Saved " + c.Slug, Public: true, Status: "active"}}
			}
			if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			current = c
			mu.Unlock()
			result, err := a.connectedApps.Detail(ctx, c.Slug)
			errorTag := ""
			if errors.Is(err, connectedapps.ErrInvalid) {
				errorTag = "invalid"
			} else if errors.Is(err, connectedapps.ErrUnavailable) {
				errorTag = "unavailable"
			} else if err != nil {
				t.Fatal(err)
			}
			if errorTag != c.Error || !reflect.DeepEqual(result, c.Expected) {
				t.Fatal("managed detail differs from independently captured original", c.Name, result, errorTag, c.Expected, c.Error)
			}
		})
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-category-options-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var options []struct {
		Name     string              `json:"name"`
		Facts    []composio.Category `json:"facts"`
		Selected string              `json:"selected"`
		Expected []composio.Category `json:"expected"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		t.Fatal(err)
	}
	for _, option := range options {
		t.Run("category-"+option.Name, func(t *testing.T) {
			state, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state.KeyRef = "credential:detail"
			state.Connections = nil
			if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			optionFacts, optionSelected = option.Facts, option.Selected
			mu.Unlock()
			got, err := a.connectedApps.View(ctx, "", option.Selected, "")
			if err != nil || !reflect.DeepEqual(got.Categories, option.Expected) {
				t.Fatal("managed categories differ from independently captured original", option.Name, got.Categories, option.Expected, err)
			}
		})
	}

}
