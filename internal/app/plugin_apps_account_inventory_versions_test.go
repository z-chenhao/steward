package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func prepareAccountInventoryVersion(t *testing.T, ctx context.Context, host rsi.Host, version string) (string, string) {
	t.Helper()
	dir := defaultFeaturePackage(t, "apps-accounts", nil)
	if version == "1.9.0" {
		replacements := map[string]string{}
		for _, name := range []string{"intent.go", "inventory.go"} {
			path, e := filepath.Abs("../plugin/appsaccounts/" + name)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			s := string(raw)
			if name == "intent.go" {
				s = strings.Replace(s, `Version: "1.8.0"`, `Version: "1.9.0"`, 1)
			} else {
				marker := "items := chooseInventory(rows, request.Connected)"
				if !strings.Contains(s, marker) {
					t.Fatal("inventory variation marker missing")
				}
				s = strings.Replace(s, marker, marker+";for l,r:=0,len(items)-1;l<r;l,r=l+1,r-1{items[l],items[r]=items[r],items[l]}", 1)
			}
			replacement := filepath.Join(t.TempDir(), name)
			if e = os.WriteFile(replacement, []byte(s), 0600); e != nil {
				t.Fatal(e)
			}
			replacements[path] = replacement
		}
		raw, _ := json.Marshal(map[string]any{"Replace": replacements})
		overlay := filepath.Join(t.TempDir(), "overlay.json")
		if e := os.WriteFile(overlay, raw, 0600); e != nil {
			t.Fatal(e)
		}
		if out, e := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-accounts").CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
		raw, e := os.ReadFile(filepath.Join(dir, "steward.plugin.json"))
		if e != nil {
			t.Fatal(e)
		}
		var m rsiplugin.Manifest
		if e = rsiplugin.Decode(raw, &m); e != nil {
			t.Fatal(e)
		}
		m.Version = version
		m, e = rsi.Seal(dir, m)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ = json.Marshal(m)
		if e = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	pkg, e := host.Install(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	graph, e := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-accounts", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-accounts", StateVersion: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	policy := rsi.Policy{ID: "inventory-owner-" + version, AllowedRequests: []string{"input.read"}}
	for _, name := range []string{"apps-cases.json", "apps-account-inventory-owner-v1.json"} {
		raw, e := os.ReadFile("../rsi/builtin/" + name)
		if e != nil {
			t.Fatal(e)
		}
		var cases []rsi.Case
		if e = json.Unmarshal(raw, &cases); e != nil {
			t.Fatal(e)
		}
		for _, c := range cases {
			if c.InstanceID != "apps-accounts" {
				continue
			}
			if version == "1.9.0" && c.Contract == contract.Inventory {
				var p contract.InventoryPlan
				if e = json.Unmarshal(c.Expected, &p); e != nil {
					t.Fatal(e)
				}
				for l, r := 0, len(p.Items)-1; l < r; l, r = l+1, r-1 {
					p.Items[l], p.Items[r] = p.Items[r], p.Items[l]
				}
				c.Expected, _ = json.Marshal(p)
			}
			policy.Cases = append(policy.Cases, c)
			seen := false
			for _, s := range policy.AllowedContracts {
				if s == c.Contract {
					seen = true
				}
			}
			if !seen {
				policy.AllowedContracts = append(policy.AllowedContracts, c.Contract)
			}
		}
	}
	if e = host.Store.PutPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	return graph.Revision, policy.ID
}
func TestAppsAccountInventoryActualIndependentVersionsFrozenAndDisable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, e := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if e != nil {
		t.Fatal(e)
	}
	secrets := memory.New()
	a, e := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	first, firstPolicy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
	second, secondPolicy := prepareAccountInventoryVersion(t, ctx, host, "1.9.0")
	activate := func(base, target, policy, id string) {
		t.Helper()
		if e = host.Propose(ctx, rsi.Candidate{ID: id, Baseline: base, Target: target, PolicyID: policy, Goal: "Independently upgrade and roll back actual account inventory"}); e != nil {
			t.Fatal(e)
		}
		decision, err := host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: base, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	activate("", first, firstPolicy, "inventory-first")
	state, e := a.store.LoadApps(ctx)
	if e != nil {
		t.Fatal(e)
	}
	state.Connections = []connectedapps.Connection{{Toolkit: "z", Name: "Zulu", Status: "pending"}, {Toolkit: "a", Name: "Alpha", Status: "active"}}
	if e = a.store.SaveApps(ctx, state, state.Revision); e != nil {
		t.Fatal(e)
	}
	state, e = a.store.LoadApps(ctx)
	if e != nil {
		t.Fatal(e)
	}
	read := func(ref string) connectedapps.AccountInventoryPolicy {
		t.Helper()
		var p connectedapps.AccountInventoryPolicy
		var err error
		if ref == "" {
			p, err = a.appsAccountInventoryWithHost(ctx, host)
		} else {
			p, err = a.appsAccountInventoryForGraph(ctx, host, ref)
		}
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	check := func(p connectedapps.AccountInventoryPolicy, first int) {
		t.Helper()
		v, err := p.Project(ctx, state, true)
		if err != nil || len(v.Rows) != 2 || v.Rows[0] != first {
			t.Fatal("wrong independent inventory", v.Rows, err)
		}
	}
	frozenFirst := read("")
	check(frozenFirst, 1)
	activate(first, second, secondPolicy, "inventory-upgrade")
	frozenSecond := read("")
	check(frozenSecond, 0)
	check(frozenFirst, 1)
	activate(second, first, firstPolicy, "inventory-rollback")
	check(read(""), 1)
	check(frozenSecond, 0)
	activate(first, second, secondPolicy, "inventory-upgrade-again")
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	a.eventsRequired.Store(false)
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	// First dependent read after recovery, without reselecting or repairing state.
	check(read(""), 0)
	check(read(first), 1)
	if _, e = host.Store.Deactivate(ctx, "inventory-disable", second); e != nil {
		t.Fatal(e)
	}
	if _, e = a.appsAccountInventoryWithHost(ctx, host); e == nil {
		t.Fatal("disabled inventory became native")
	}
	check(read(first), 1)
	check(read(second), 0)
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	a.eventsRequired.Store(false)
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	if _, e = a.appsAccountInventoryWithHost(ctx, host); e == nil {
		t.Fatal("cold disable became native")
	}
	check(read(first), 1)
	check(read(second), 0)
}
