package rsi_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func computerBootstrapPackage(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if output, err := exec.Command("go", "build", "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/computer-use").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err := os.ReadFile("../../plugins/computer-use/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestComputerBootstrapVerifiesActualPackageAndPreservesOwnerSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	directories := builtin.Directories{DOCX: docxPackageSource(t), Computer: computerBootstrapPackage(t)}
	t.Run("fresh-and-deactivated", func(t *testing.T) {
		f := newFixture(t)
		if err := builtin.EnsureDefault(ctx, f.host, directories); err != nil {
			t.Fatal(err)
		}
		first, err := f.host.Store.Status(ctx)
		if err != nil || first.Epoch != 1 || first.Revision == "" {
			t.Fatal(first, err)
		}
		graph, err := f.host.Store.Graph(ctx, first.Revision)
		if err != nil || len(graph.Graph.Bindings) != 2 {
			t.Fatal(graph, err)
		}
		for _, binding := range graph.Graph.Bindings {
			if binding.InstanceID != "computer-use" {
				continue
			}
			var config map[string]string
			if err = json.Unmarshal(binding.Config, &config); err != nil || len(config) != 2 || config["browser_upload_driver"] != macosvm.BrowserUploadResourceHash() || config["browser_snapshot_driver"] != macosvm.BrowserSnapshotResourceHash() {
				t.Fatal("default selection lost exact fixed resources", config, err)
			}
		}
		candidate, err := f.host.Store.Candidate(ctx, "bootstrap-docx-"+first.Revision[len("sha256:"):])
		if err != nil {
			t.Fatal(err)
		}
		policy, err := f.host.Store.Policy(ctx, candidate.PolicyID)
		if err != nil || len(policy.Cases) != 62 || len(policy.AllowedContracts) != 12 {
			t.Fatal("Host-owned acceptance did not cover every Computer service", len(policy.Cases), policy.AllowedContracts, err)
		}
		output, err := f.host.ReadService(ctx, first.Revision, contract.BrowserProjectionContract, json.RawMessage(`{"body_units":17,"rows":[{"role_units":6,"aria_units":5,"inner_units":7,"placeholder_units":3}]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		var view contract.BrowserProjection
		if err = json.Unmarshal(output, &view); err != nil || view.BodyUnits != 17 || len(view.Rows) != 1 || view.Rows[0].Label != "aria" || view.Rows[0].LabelUnits != 5 || view.Rows[0].Index != 0 {
			t.Fatal("real selected process did not preserve original default policy", string(output), err)
		}
		if err = builtin.EnsureDefault(ctx, f.host, directories); err != nil {
			t.Fatal(err)
		}
		again, _ := f.host.Store.Status(ctx)
		if again != first {
			t.Fatal("restart replaced initial selection", first, again)
		}
		if _, err = f.host.Store.Deactivate(ctx, "owner-disables-default", first.Revision); err != nil {
			t.Fatal(err)
		}
		disabled, _ := f.host.Store.Status(ctx)
		if err = builtin.EnsureDefault(ctx, f.host, directories); err != nil {
			t.Fatal(err)
		}
		after, _ := f.host.Store.Status(ctx)
		if after != disabled {
			t.Fatal("restart reactivated disabled Computer", after, disabled)
		}
	})
	t.Run("existing-choice", func(t *testing.T) {
		f := newFixture(t)
		if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: directories.DOCX}); err != nil {
			t.Fatal(err)
		}
		before, _ := f.host.Store.Status(ctx)
		if err := builtin.EnsureDefault(ctx, f.host, directories); err != nil {
			t.Fatal(err)
		}
		after, _ := f.host.Store.Status(ctx)
		if after != before {
			t.Fatal("new distribution silently appended Computer to an existing graph", after, before)
		}
	})
	t.Run("corrupted-computer-refuses-partial-selection", func(t *testing.T) {
		f := newFixture(t)
		broken := t.TempDir()
		for _, name := range []string{"plugin", "steward.plugin.json"} {
			raw, err := os.ReadFile(filepath.Join(directories.Computer, name))
			if err != nil {
				t.Fatal(err)
			}
			if name == "plugin" {
				raw = append(raw, []byte("unsealed change")...)
			}
			if err = os.WriteFile(filepath.Join(broken, name), raw, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: directories.DOCX, Computer: broken}); err == nil {
			t.Fatal("damaged Computer package accepted")
		}
		status, err := f.host.Store.Status(ctx)
		if err != nil || status.Epoch != 0 || status.Revision != "" {
			t.Fatal("bad Computer package selected a partial composition", status, err)
		}
	})
	t.Run("guardian-stop", func(t *testing.T) {
		f := newFixture(t)
		if err := f.host.Store.Stop(ctx, true); err != nil {
			t.Fatal(err)
		}
		before, _ := f.host.Store.Status(ctx)
		if err := builtin.EnsureDefault(ctx, f.host, directories); err != nil {
			t.Fatal(err)
		}
		after, _ := f.host.Store.Status(ctx)
		if after != before || !after.Stopped || after.Revision != "" {
			t.Fatal("bootstrap bypassed Guardian stop", after, before)
		}
	})
}
