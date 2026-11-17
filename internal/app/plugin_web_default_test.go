package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	extract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func defaultFeaturePackage(t *testing.T, name string, module []byte) string {
	t.Helper()
	directory := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/"+name).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/" + name + "/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if module != nil {
		if err := os.WriteFile(filepath.Join(directory, extract.ModuleFile), module, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if name == "apps-interface" {
		copyInterfaceViews(t, directory, manifest)
	}
	if name == "capability-settings" {
		copyCapabilityAssets(t, directory)
	}
	if name == "model-connections" {
		if err := os.MkdirAll(filepath.Join(directory, "views"), 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile("../plugin/modelconnections/view/models.html")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "views/models.html"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		raw, err = os.ReadFile("../plugin/modelconnections/view/copy.json")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "views/copy.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if name == "memory-inspector" {
		if err := os.MkdirAll(filepath.Join(directory, "views"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"memory.html", "memory.css", "copy.json", "memory-worker.js"} {
			raw, err := os.ReadFile(filepath.Join("../plugin/memoryinspector/view", file))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(directory, "views", file), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func defaultWebFixture(t *testing.T) (config.Config, builtin.Directories, string) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	source, _ := filepath.Abs("../plugin/webextract/extract.mjs")
	deps, _ := filepath.Abs("../../runtime/javascript/node_modules")
	module, err := parserbundle.Bundle(context.Background(), source, deps)
	if err != nil {
		t.Fatal(err)
	}
	d := builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), Web: defaultFeaturePackage(t, "web-access", nil), Extract: defaultFeaturePackage(t, "web-extract", module)}
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	return cfg, d, carrier
}

func TestWebDefaultCustomSourceColdReopenRetainsOriginalModuleAndInterpreter(t *testing.T) {
	ctx := context.Background()
	cfg, d, carrier := defaultWebFixture(t)
	trusted := t.TempDir()
	script := filepath.Join(trusted, "src", "owner.mjs")
	command := filepath.Join(trusted, "node")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := os.ReadFile(node)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		command: engine,
		script:  []byte(`import {word} from 'owner-method';let raw='';for await(const chunk of process.stdin)raw+=chunk;const q=JSON.parse(raw);process.stdout.write(JSON.stringify({title:word,text:'input:'+Buffer.from(q.data,'base64').length+':'+ 'x'.repeat(3*1024*1024)}));`),
		filepath.Join(trusted, "node_modules", "owner-method", "package.json"): []byte(`{"name":"owner-method","version":"1.0.0","type":"module","exports":"./index.mjs"}`),
		filepath.Join(trusted, "node_modules", "owner-method", "index.mjs"):    []byte(`export const word='original owner method';`),
		filepath.Join(trusted, "node_modules", "owner-method", "LICENSE"):      []byte("Owner fixture permission\n"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if path == command {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	cfg.WebAccess.ExtractCommand, cfg.WebAccess.ExtractScript = command, script
	a, err := OpenControl(ctx, cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := a.Plugins()
	h.Catalog.Launcher = carrier
	value, err := prepareWebDefault(ctx, h, cfg, d.Extract)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if len(value.Module) == 0 || len(value.Expected) != 2 || value.Config.SourceIdentity != cfg.WebAccess.ParserSourceIdentity() {
		a.Close()
		t.Fatal("custom source substituted or unbound")
	}
	if err := builtin.EnsureDefault(ctx, h, d, value); err != nil {
		a.Close()
		t.Fatal(err)
	}
	original, err := h.Store.Status(ctx)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("replacement source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("replacement interpreter"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(trusted, "node_modules")); err != nil {
		t.Fatal(err)
	}
	a, err = OpenControl(ctx, cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// This is the first dependent behavior after a real close/open. There is no
	// policy select, re-capture or fixture repair after the cold reopen.
	h = a.Plugins()
	h.Catalog.Launcher = carrier
	response, err := h.ExtractService(ctx, original.Revision, extract.Request{InputID: "document", URL: "https://example.com/original", MediaType: "text/html"}, bytes.Repeat([]byte("x"), 8<<20), func(context.Context) error { return nil })
	if err != nil || response.Title != "original owner method" || !strings.HasPrefix(response.Text, "input:8388608:") || len(response.Text) <= 3<<20 {
		t.Fatal(response.Title, len(response.Text), err)
	}
	if err := a.prepareBuiltinPlugins(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	after, err := h.Store.Status(ctx)
	if err != nil || after != original {
		t.Fatal("cold reopen changed original selection", after, original, err)
	}
}

func TestWebDefaultInvalidOptionalSourceKeepsOtherManagedMethodsAvailable(t *testing.T) {
	ctx := context.Background()
	cfg, d, carrier := defaultWebFixture(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "invalid.mjs")
	if err := os.WriteFile(script, []byte("export { broken syntax"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.WebAccess.ExtractCommand, cfg.WebAccess.ExtractScript = node, script
	a, err := OpenControl(ctx, cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Plugins()
	h.Catalog.Launcher = carrier
	value, err := prepareWebDefault(ctx, h, cfg, d.Extract)
	if err != nil {
		t.Fatal(err)
	}
	if value.Config.RuntimeRevision != extract.UnavailableRuntime || value.Config.UnavailableError != "WEB_EXTRACTION_FAILED" {
		t.Fatal(value.Config)
	}
	if err := builtin.EnsureDefault(ctx, h, d, value); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output, err := h.ReadService(ctx, status.Revision, "steward.web-content-route/1", json.RawMessage(`{"operation":"fetch","media_type":"text/html","mode":"","document_available":false}`), nil)
	if err != nil || !strings.Contains(string(output), `"html"`) {
		t.Fatal(string(output), err)
	}
	input := builtin.WebExtractInputs()[0]
	_, err = h.ExtractService(ctx, status.Revision, extract.Request{InputID: "document", URL: input.URL, MediaType: input.MediaType}, input.Data, func(context.Context) error { return nil })
	if err == nil || err.Error() != "WEB_EXTRACTION_FAILED" {
		t.Fatal(err)
	}
}
