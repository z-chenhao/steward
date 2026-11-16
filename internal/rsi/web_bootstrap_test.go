package rsi_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	extract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func bootstrapPackage(t *testing.T, name string, module []byte) string {
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
		for _, resource := range manifest.Resources {
			raw, err := os.ReadFile(filepath.Join("../../plugins", name, resource.Path))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, resource.Path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
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

func TestWebBootstrapActualMethodsUnavailableResourceAndOwnerSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	source, _ := filepath.Abs("../plugin/webextract/extract.mjs")
	deps, _ := filepath.Abs("../../runtime/javascript/node_modules")
	module, err := parserbundle.Bundle(ctx, source, deps)
	if err != nil {
		t.Fatal(err)
	}
	d := builtin.Directories{DOCX: docxPackageSource(t), Web: bootstrapPackage(t, "web-access", nil), Extract: bootstrapPackage(t, "web-extract", module)}
	for _, mode := range []string{"configured", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			value := builtin.WebExtract{Config: extract.Config{RuntimeRevision: extract.UnavailableRuntime, SourceIdentity: "owner-test-source"}}
			if mode == "configured" {
				node, err := exec.LookPath("node")
				if err != nil {
					t.Fatal(err)
				}
				node, err = filepath.EvalSymlinks(node)
				if err != nil {
					t.Fatal(err)
				}
				resource, err := f.host.CaptureWebParser(ctx, node, module)
				if err != nil {
					t.Fatal(err)
				}
				value.Config.RuntimeRevision = resource.Digest()
			}
			if err := builtin.EnsureDefault(ctx, f.host, d, value); err != nil {
				t.Fatal(err)
			}
			status, err := f.host.Store.Status(ctx)
			if err != nil || status.Epoch != 1 || status.Revision == "" {
				t.Fatal(status, err)
			}
			graph, err := f.host.Store.Graph(ctx, status.Revision)
			if err != nil || len(graph.Graph.Bindings) != 3 {
				t.Fatal(graph, err)
			}
			for _, input := range builtin.WebExtractInputs() {
				response, err := f.host.ExtractService(ctx, status.Revision, extract.Request{InputID: "document", URL: input.URL, MediaType: input.MediaType}, input.Data, func(context.Context) error { return nil })
				if mode == "configured" && (err != nil || response.Text == "") {
					t.Fatal(response, err)
				}
				if mode == "unavailable" && (err == nil || err.Error() != "WEB_EXTRACTOR_UNAVAILABLE") {
					t.Fatal(response, err)
				}
			}
			// A restart must not re-read missing current packages or reselect work.
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing/current/distribution"}); err != nil {
				t.Fatal(err)
			}
			again, _ := f.host.Store.Status(ctx)
			if again != status {
				t.Fatal("restart rewrote selection", again, status)
			}
			if _, err := f.host.Store.Deactivate(ctx, "owner-disable-web-default", status.Revision); err != nil {
				t.Fatal(err)
			}
			disabled, _ := f.host.Store.Status(ctx)
			if err := builtin.EnsureDefault(ctx, f.host, d, value); err != nil {
				t.Fatal(err)
			}
			again, _ = f.host.Store.Status(ctx)
			if again != disabled {
				t.Fatal("restart reenabled disabled plugins", again, disabled)
			}
		})
	}
}
