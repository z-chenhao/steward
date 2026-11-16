package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestPluginSealDoesNotInitializeUserState(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "private-data")
	t.Setenv("STEWARD_DATA_ROOT", dataRoot)
	packageRoot := filepath.Join(root, "package")
	if err := os.Mkdir(packageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../plugins/docx-reader/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(packageRoot, "steward.plugin.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(packageRoot, "plugin"), []byte("built artifact"), 0700); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if err = run(context.Background(), []string{"-config", filepath.Join(root, "missing-config.yaml"), "plugin", "seal", packageRoot}, bytes.NewReader(nil), &out, &diagnostics); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	var sealed rsiplugin.Manifest
	if err = rsiplugin.Decode(out.Bytes(), &sealed); err != nil || len(sealed.Files) != 1 {
		t.Fatal("package was not sealed", sealed, err)
	}
	if _, err = os.Stat(dataRoot); !os.IsNotExist(err) {
		t.Fatal("sealing opened user state", err)
	}
}

func TestPluginParserBundleIsOfflineAndPreservesOutputOnFailure(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "private-data")
	t.Setenv("STEWARD_DATA_ROOT", dataRoot)
	source, output, deps := filepath.Join(root, "owner.mjs"), filepath.Join(root, "sealed.mjs"), filepath.Join(root, "node_modules")
	if err := os.Mkdir(deps, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(`process.stdout.write('sealed owner');`), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-config", filepath.Join(root, "missing-config.yaml"), "plugin", "parser-bundle", source, output, deps}
	var out, diagnostics bytes.Buffer
	if err := run(context.Background(), args, bytes.NewReader(nil), &out, &diagnostics); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	original, err := os.ReadFile(output)
	if err != nil || len(original) == 0 {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if result, err := exec.Command(node, output).CombinedOutput(); err != nil || string(result) != "sealed owner" {
		t.Fatal(string(result), err)
	}
	if err := os.WriteFile(source, []byte(`import 'MISSING_OWNER_DEPENDENCY';`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, bytes.NewReader(nil), &out, &diagnostics); err == nil {
		t.Fatal("invalid source accepted")
	}
	retained, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(retained, original) {
		t.Fatal("failed build replaced original module", err)
	}
	if _, err := os.Stat(dataRoot); !os.IsNotExist(err) {
		t.Fatal("offline build opened user state", err)
	}
}

func TestPluginPolicyAcceptsIndependentDocumentBeyondOneFrame(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenControl(context.Background(), cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	expected, _ := json.Marshal(map[string]string{"text": strings.Repeat("<", 200000)})
	policy := rsi.Policy{ID: "large-owner-gate", Cases: []rsi.Case{{InstanceID: "feature", Contract: "steward.example/1", Input: json.RawMessage(`{}`), Expected: expected}}}
	raw, _ := json.Marshal(policy)
	if len(raw) <= rsiplugin.MaxFrameBytes {
		t.Fatal("fixture did not exceed frame")
	}
	if pluginInput(bytes.NewReader(raw), new(rsi.Policy)) == nil {
		t.Fatal("ordinary RPC/control command widened")
	}
	flags := flag.NewFlagSet("steward", flag.ContinueOnError)
	if err = flags.Parse([]string{"plugin", "policy"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = runPlugin(context.Background(), a, flags, bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	stored, err := a.Plugins().Store.Policy(context.Background(), policy.ID)
	if err != nil || len(stored.Cases) != 1 || string(stored.Cases[0].Expected) != string(expected) {
		t.Fatal("independent expected bytes changed", err)
	}
}

func TestPluginParserResourceControlReturnsExactSourceBinding(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), "owner-parser.mjs")
	if err = os.WriteFile(module, []byte(`process.stdout.write(JSON.stringify({title:'owner',text:'original'}));`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.WebAccess.ExtractCommand, cfg.WebAccess.ExtractScript = node, module
	application, err := app.OpenControl(context.Background(), cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	var output bytes.Buffer
	if err = runParserResource(context.Background(), application, cfg, module, &output); err != nil {
		t.Fatal(err)
	}
	var binding struct {
		RuntimeRevision string `json:"runtime_revision"`
		SourceIdentity  string `json:"source_identity"`
	}
	if json.Unmarshal(output.Bytes(), &binding) != nil || !strings.HasPrefix(binding.RuntimeRevision, "sha256:") || binding.SourceIdentity != cfg.WebAccess.ParserSourceIdentity() {
		t.Fatal(output.String())
	}
	if strings.Contains(output.String(), node) || strings.Contains(output.String(), module) {
		t.Fatal("resource control exposed source paths")
	}
}
