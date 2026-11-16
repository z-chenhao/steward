package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/terminalinterface"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTerminalInterfaceRetainsFourOriginalSetupProfilesAndOutput(t *testing.T) {
	var cases []struct {
		Name     string
		Lines    []string
		Catalog  []llm.CatalogModel
		Expected llm.ProfileInput
		Output   string
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/model-connections-original-v1/terminal-setup-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) != 4 {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			lines := make(chan inputEvent, len(c.Lines))
			for _, line := range c.Lines {
				lines <- inputEvent{kind: inputLine, text: line}
			}
			close(lines)
			models := &captureSetupModels{}
			var output bytes.Buffer
			u := terminal{Session: Session{Models: models, Accounts: captureSetupAccounts{}, Discover: func(context.Context, string, string) ([]llm.CatalogModel, error) { return c.Catalog, nil }, AccountCatalog: func(context.Context, string) ([]llm.CatalogModel, error) { return c.Catalog, nil }, Interface: func(_ context.Context, q contract.Query) (contract.Result, bool, error) {
				out, e := owner.Project(q)
				return out, true, e
			}}, lines: lines, out: &output}
			if err := u.setup(context.Background()); !errors.Is(err, errCancelled) || !reflect.DeepEqual(models.input, c.Expected) || output.String() != c.Output {
				t.Fatal("original setup drift", models.input, output.String(), err)
			}
		})
	}
}
func TestTerminalInterfaceCannotInventChoiceOrFallBackAfterSelection(t *testing.T) {
	for _, kind := range []string{"wrong-index", "manual-on-account", "missing", "unmanaged", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			lines := make(chan inputEvent, 1)
			lines <- inputEvent{kind: inputLine, text: "a"}
			close(lines)
			var output bytes.Buffer
			u := terminal{lines: lines, out: &output}
			u.Interface = func(_ context.Context, q contract.Query) (contract.Result, bool, error) {
				if q.Operation == "view" {
					v := owner.View()
					return contract.Result{View: &v}, true, nil
				}
				if q.Facts == nil || q.Facts.Count != 2 || q.Facts.Exact != 0 || q.Facts.Number != -1 || q.Facts.Manual {
					t.Fatal("original choice evidence lost", q)
				}
				switch kind {
				case "unmanaged":
					return contract.Result{}, false, nil
				case "stopped":
					return contract.Result{}, true, llm.ProviderUnavailable
				case "missing":
					return contract.Result{}, true, nil
				case "manual-on-account":
					return contract.Result{Choice: &contract.Choice{Kind: "manual"}}, true, nil
				}
				return contract.Result{Choice: &contract.Choice{Kind: "index", Index: 1}}, true, nil
			}
			if _, err := u.chooseModel(context.Background(), []llm.CatalogModel{{ModelID: "a"}, {ModelID: "b"}}, false); err == nil {
				t.Fatal("invalid policy changed explicit selection")
			}
		})
	}
}
func TestTerminalInterfaceUnavailableViewCannotReachControl(t *testing.T) {
	for _, kind := range []string{"missing", "extra-format", "escape", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			var output bytes.Buffer
			s := Session{Interface: func(context.Context, contract.Query) (contract.Result, bool, error) {
				v := owner.View()
				switch kind {
				case "missing":
					return contract.Result{}, true, nil
				case "stopped":
					return contract.Result{}, true, llm.ProviderUnavailable
				case "extra-format":
					v.InputPrompt = "%s"
				case "escape":
					v.Help = "\x1b[2J"
				}
				return contract.Result{View: &v}, true, nil
			}}
			// Nil State would panic if the rejected view reached diagnostics or setup.
			if err := s.Run(context.Background(), io.NopCloser(strings.NewReader("/setup\n")), &output); !errors.Is(err, llm.ProviderUnavailable) || output.Len() != 0 {
				t.Fatal("invalid view reached native control", output.String(), err)
			}
		})
	}
}
func TestTerminalInterfaceNumericAmbiguityBindsOriginalCatalogue(t *testing.T) {
	lines := make(chan inputEvent, 1)
	lines <- inputEvent{kind: inputLine, text: "2"}
	close(lines)
	u := terminal{lines: lines, out: io.Discard}
	u.Interface = func(_ context.Context, q contract.Query) (contract.Result, bool, error) {
		out, err := owner.Project(q)
		if q.Operation == "choose-model" {
			if q.Facts.Exact != 0 || q.Facts.Number != 1 {
				t.Fatal(q)
			}
			out.Choice = &contract.Choice{Kind: "index", Index: q.Facts.Number}
		}
		return out, true, err
	}
	value, err := u.chooseModel(context.Background(), []llm.CatalogModel{{ModelID: "2"}, {ModelID: "original-second"}}, false)
	if err != nil || value != "original-second" {
		t.Fatal("versioned choice did not bind original index", value, err)
	}
}
