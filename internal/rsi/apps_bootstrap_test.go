package rsi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestConnectedAppsBootstrapVerifiesOfflineAndPreservesOwnerSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	directories := builtin.Directories{DOCX: docxPackageSource(t), Apps: bootstrapPackage(t, "connected-apps", nil)}
	for _, mode := range []string{"new", "prior-selection", "deactivated", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode == "prior-selection" {
				if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: directories.DOCX}); err != nil {
					t.Fatal(err)
				}
			}
			if err := builtin.EnsureDefault(ctx, f.host, directories); err != nil {
				t.Fatal("independent owner cases did not validate without real accounts or HTTP", err)
			}
			selected, err := f.host.Store.Status(ctx)
			if err != nil || selected.Epoch != 1 || selected.Revision == "" {
				t.Fatal(selected, err)
			}
			graph, err := f.host.Store.Graph(ctx, selected.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "prior-selection" {
				if len(graph.Graph.Bindings) != 1 || graph.Graph.Bindings[0].InstanceID != "docx-reader" {
					t.Fatal("new distribution silently added Apps to an existing choice", graph)
				}
			} else {
				if len(graph.Graph.Bindings) != 2 {
					t.Fatal("Apps method package did not participate in actual initial assembly", graph)
				}
				raw, err := f.host.ReadService(ctx, selected.Revision, contract.Guidance, json.RawMessage(`{}`), nil)
				var plan contract.GuidancePlan
				if err != nil || rsiplugin.DecodeResult(raw, &plan) != nil || plan.Namespace.Before != "Connected app account: " || plan.Search.Before != "Find available operations for " || plan.Execute.Before != "Execute one operation on " {
					t.Fatal("actual default guidance did not match owner expectations", string(raw), err)
				}
				if _, err := f.host.ReadService(ctx, selected.Revision, contract.Method, json.RawMessage(`{"operation":"execute"}`), nil); err == nil {
					t.Fatal("bootstrap granted a real account or an operation source")
				}
			}
			if mode == "deactivated" {
				if _, err := f.host.Store.Deactivate(ctx, "apps-default-owner-disable", selected.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stopped" {
				if err := f.host.Store.Stop(ctx, true); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// First restart action must preserve selection before touching any
			// current packages, even if this distribution is no longer readable.
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing/current/package", Apps: "/missing/current/apps"}); err != nil {
				t.Fatal(err)
			}
			after, err := f.host.Store.Status(ctx)
			if err != nil || after != before {
				t.Fatal("restart replaced the explicit graph, deactivation or stop", before, after, err)
			}
			var opaque int
			if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call`).Scan(&opaque); err != nil || opaque != 0 {
				t.Fatal("pure bootstrap created a second operation ledger", opaque, err)
			}
		})
	}
}
