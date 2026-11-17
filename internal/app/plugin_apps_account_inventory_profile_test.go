package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAppsAccountInventoryOriginalVersionLabelCannotForgeProtocolProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, memory.New())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, e := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	current, currentPolicy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
	activateInterface(t, ctx, host, "", current, currentPolicy, "inventory-profile-current")
	// The executable is the original seven-service producer and honestly says
	// version1.0. Its different, valid optional configuration schema means it is
	// not the captured historical protocol profile, despite the old version tag.
	dir := accountIntentPackage(t, "1.0.0")
	raw, err := os.ReadFile(filepath.Join(dir, "steward.plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ConfigSchema = json.RawMessage(`{"type":"object","properties":{"different_profile":{"type":"boolean"}},"additionalProperties":false}`)
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	pkg, err := host.Install(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-accounts", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-accounts", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../rsi/builtin/apps-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []rsi.Case
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	policy := rsi.Policy{ID: "inventory-different-old-profile", AllowedRequests: []string{"input.read"}}
	for _, c := range cases {
		if c.InstanceID != "apps-accounts" {
			continue
		}
		policy.Cases = append(policy.Cases, c)
		seen := false
		for _, x := range policy.AllowedContracts {
			if x == c.Contract {
				seen = true
			}
		}
		if !seen {
			policy.AllowedContracts = append(policy.AllowedContracts, c.Contract)
		}
	}
	if err = host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateInterface(t, ctx, host, current, graph.Revision, policy.ID, "inventory-different-profile")
	if _, err = a.appsAccountInventoryWithHost(ctx, host); err == nil {
		t.Fatal("old version tag granted historical interpretation to another protocol profile")
	}
	if _, err = a.appsAccountInventoryForGraph(ctx, host, graph.Revision); err == nil {
		t.Fatal("explicit reference bypassed historical profile check")
	}
	if _, err = a.appsAccountInventoryForGraph(ctx, host, current); err != nil {
		t.Fatal("invalid old profile broke retained managed reference", err)
	}
}
