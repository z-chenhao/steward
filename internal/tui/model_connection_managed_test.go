package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"os"
	"reflect"
	"strings"
	"testing"
)

type captureSetupModels struct {
	Models
	input llm.ProfileInput
}

func (m *captureSetupModels) AddProfile(_ context.Context, p llm.ProfileInput) (llm.Profile, error) {
	m.input = p
	return llm.Profile{}, errCancelled
}

type captureSetupAccounts struct {
	Accounts
	logins *int
}

func (a captureSetupAccounts) Login(context.Context, string, func(llm.AuthEvent)) (llm.AuthConnection, error) {
	if a.logins != nil {
		*a.logins++
	}
	return llm.AuthConnection{ConnectionID: "original-account"}, nil
}
func TestModelConnectionTerminalSetupMatches4NativeProfilesAndPrompts(t *testing.T) {
	type result struct {
		Name     string             `json:"name"`
		Lines    []string           `json:"lines"`
		Catalog  []llm.CatalogModel `json:"catalog"`
		Expected llm.ProfileInput   `json:"expected"`
		Output   string             `json:"output"`
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/model-connections-original-v1/terminal-setup-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []result
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatal("native setup cases missing", len(cases))
	}
	for _, c := range cases {
		for _, mode := range []string{"owned", "independent", "stopped", "invalid-endpoint", "invalid-limits", "invalid-wire", "missing", "historical"} {
			t.Run(c.Name+"/"+mode, func(t *testing.T) {
				lines := make(chan inputEvent, len(c.Lines))
				for _, line := range c.Lines {
					lines <- inputEvent{kind: inputLine, text: line}
				}
				close(lines)
				models := &captureSetupModels{}
				var output bytes.Buffer
				calls := 0
				catalogReads := 0
				loginReads := 0
				catalog := func(context.Context, string) ([]llm.CatalogModel, error) { catalogReads++; return c.Catalog, nil }
				u := terminal{Session: Session{Models: models, Accounts: captureSetupAccounts{logins: &loginReads}, AccountCatalog: catalog, Discover: func(context.Context, string, string) ([]llm.CatalogModel, error) {
					catalogReads++
					return c.Catalog, nil
				}}, lines: lines, out: &output}
				u.ConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
					calls++
					if f.Preset != contract.TerminalSetup || f.AccountBacked != (c.Name == "account") {
						t.Fatal("terminal facts lost", f)
					}
					out := owner.ProjectConnection(f)
					switch mode {
					case "independent":
						out.ContextWindow = 96000
						out.TokenLimit = 524288
						out.EndpointDefault = "http://127.0.0.1:9000/v1"
					case "stopped":
						return out, true, llm.ErrNoBridge
					case "invalid-endpoint":
						out.EndpointDefault = "file:///private"
					case "invalid-limits":
						out.TokenLimit = -1
					case "invalid-wire":
						out.WireAPI = "unknown"
					case "missing":
						out = contract.ConnectionDefaults{}
					}
					return out, mode != "historical", nil
				}
				err := u.setup(context.Background())
				if calls != 1 {
					t.Fatal("native default used without owner", calls)
				}
				if mode == "stopped" || mode == "invalid-endpoint" || mode == "invalid-limits" || mode == "invalid-wire" || mode == "missing" {
					if err == nil || models.input.ModelID != "" || catalogReads != 0 || loginReads != 0 {
						t.Fatal("rejected recipe reached authority", err, models.input, catalogReads)
					}
					return
				}
				if err != errCancelled {
					t.Fatal("capture boundary changed", err, output.String())
				}
				want := c.Expected
				if mode == "independent" {
					if c.Name == "manual" {
						want.ContextWindow = 96000
					}
					if c.Name != "account" && c.Name != "explicit" {
						want.Endpoint = "http://127.0.0.1:9000/v1"
					}
				}
				if !reflect.DeepEqual(models.input, want) {
					t.Fatal("profile identity/catalog/user bounds changed", models.input, want)
				}
				if mode != "independent" && output.String() != c.Output {
					t.Fatal("default terminal presentation changed", output.String(), c.Output)
				}
				if mode == "independent" && c.Name != "account" && !strings.Contains(output.String(), "http://127.0.0.1:9000/v1") {
					t.Fatal("owner default not visible before consent", output.String())
				}
			})
		}
	}
}
