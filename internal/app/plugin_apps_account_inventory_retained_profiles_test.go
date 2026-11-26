package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"path/filepath"
	"testing"
)

func TestAppsAccountInventoryAllRetainedPreServiceProfilesKeepOriginalColdRead(t *testing.T) {
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	raw, e := os.ReadFile("../rsi/builtin/apps-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var owner []rsi.Case
	if e = json.Unmarshal(raw, &owner); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"apps-accounts-pre-auth-v1", "apps-accounts-pre-session-v1", "apps-accounts-pre-check-v1", "apps-accounts-pre-close-v1", "apps-accounts-pre-reconnect-v1", "apps-accounts-pre-settings-v1"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
			if err != nil {
				t.Fatal(err)
			}
			secrets := memory.New()
			a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			a.eventsRequired.Store(false)
			host := a.Plugins()
			host.Catalog.Launcher = carrier
			current, currentPolicy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
			activateInterface(t, ctx, host, "", current, currentPolicy, "inventory-retained-current")
			pkg, err := host.Install(ctx, retainedAccountPackage(t, name))
			if err != nil {
				t.Fatal(err)
			}
			old, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-accounts", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-accounts", StateVersion: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			policy := rsi.Policy{ID: "inventory-retained-" + name, AllowedRequests: []string{"input.read"}}
			allowed := map[string]bool{}
			for _, s := range pkg.Manifest.Provides {
				policy.AllowedContracts = append(policy.AllowedContracts, s.Contract)
				allowed[s.Contract] = true
			}
			for _, c := range owner {
				if c.InstanceID == "apps-accounts" && allowed[c.Contract] {
					policy.Cases = append(policy.Cases, c)
				}
			}
			if err = host.Store.PutPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			activateInterface(t, ctx, host, current, old.Revision, policy.ID, "inventory-retained-original")
			state, err := a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state.Connections = []connectedapps.Connection{{Toolkit: "z", Name: "Zulu", Status: "pending"}, {Toolkit: "a", Name: "Alpha", Status: "active", Public: true}}
			if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
				t.Fatal(err)
			}
			state, err = a.store.LoadApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				p, e := a.appsAccountInventoryWithHost(ctx, host)
				if e != nil {
					t.Fatal("known retained profile rejected", e)
				}
				v, e := p.Project(ctx, state, true)
				if e != nil || len(v.Rows) != 2 || v.Rows[0] != 1 || v.Rows[1] != 0 {
					t.Fatal("retained profile changed original inventory", v.Rows, e)
				}
				flags := v.States([]string{"a", "z"})
				if !flags[0].PublicActive || !flags[1].Pending {
					t.Fatal("retained profile changed original state", flags)
				}
			}
			check()
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
			if err != nil {
				t.Fatal(err)
			}
			a.eventsRequired.Store(false)
			host = a.Plugins()
			host.Catalog.Launcher = carrier
			// Original graph and state are consumed immediately after real Store recovery.
			check()
		})
	}
}
