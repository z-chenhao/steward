package rsi_test

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestMemoryInspectorBootstrapIndependentOwnerCasesAndRetainedSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	dirs := builtin.Directories{DOCX: docxPackageSource(t), MemoryInspector: memoryInspectorPackage(t)}
	for _, mode := range []string{"new", "prior-selection", "deactivated", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode == "prior-selection" {
				if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: dirs.DOCX}); err != nil {
					t.Fatal(err)
				}
			}
			if err := builtin.EnsureDefault(ctx, f.host, dirs); err != nil {
				t.Fatal("independent captured Owner cases rejected actual package", err)
			}
			selected, err := f.host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := f.host.Store.Graph(ctx, selected.Revision)
			if err != nil {
				t.Fatal(err)
			}
			count := 2
			if mode == "prior-selection" {
				count = 1
			}
			if len(graph.Graph.Bindings) != count {
				t.Fatal("default silently changed retained selection", graph)
			}
			if mode != "prior-selection" {
				raw, err := f.host.ReadService(ctx, selected.Revision, contract.Presentation, json.RawMessage(`{"navigation":[]}`), nil)
				var result contract.Result
				if err != nil || rsiplugin.DecodeResult(raw, &result) != nil || result.Labels.DefaultStatus != "active" || result.Program.Source == "" {
					t.Fatal("default presentation unavailable", err)
				}
			}
			if mode == "deactivated" {
				if _, err := f.host.Store.Deactivate(ctx, "memory-inspector-disable", selected.Revision); err != nil {
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
			// First restart action uses no current package and must not repair a selection.
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing/docx", MemoryInspector: "/missing/inspector"}); err != nil {
				t.Fatal(err)
			}
			after, err := f.host.Store.Status(ctx)
			if err != nil || after != before {
				t.Fatal("restart replaced Owner selection", before, after, err)
			}
			var calls int
			if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call`).Scan(&calls); err != nil || calls != 0 {
				t.Fatal("pure initialization created effect ledger", calls, err)
			}
		})
	}
}
