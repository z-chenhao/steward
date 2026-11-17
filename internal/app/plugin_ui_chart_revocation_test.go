package app

import (
	"context"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/memory"
)

// The system gate supplies a real delivered Chart and disabled graph. A copy
// allows the actual owner Forget control without modifying original evidence.
func TestUIChartActualForgetRevokesOriginalViewAndExport(t *testing.T) {
	path, ref := os.Getenv("STEWARD_TEST_CHART_CONFIG"), os.Getenv("STEWARD_TEST_CHART_REF")
	if path == "" {
		t.Skip("requires actual managed Chart App system fixture")
	}
	if ref == "" {
		t.Fatal("original Chart ref required")
	}
	original, err := config.Load(path, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := original
	cfg.App.DataRoot = t.TempDir()
	if err = os.CopyFS(cfg.App.DataRoot, os.DirFS(original.App.DataRoot)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_TEST_CHART_MEMORY_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	cfg.Memory = &memory.Config{EncryptionKeyRef: "env:STEWARD_TEST_CHART_MEMORY_KEY"}
	ctx := context.Background()
	app, err := OpenControl(ctx, cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.runtimeRuns().ConfigureMemory(app.memory, memory.Vault{Secrets: app.control.Secrets, Ref: cfg.Memory.EncryptionKeyRef})
	before, err := app.Plugins().Store.Status(ctx)
	if err != nil || before.Revision != "" {
		t.Fatal("cold control reactivated graph", before, err)
	}
	if view, err := app.WebChartView(ctx, ref); err != nil || view.Worker == "" {
		t.Fatal("original Chart unavailable before Forget", err)
	}
	if facts, err := app.WebChartExport(ctx, ref, []int{0}); err != nil || len(facts.Rows) != 1 || string(facts.Rows[0][0]) != `"A"` {
		t.Fatal("original export unavailable", facts, err)
	}
	result, err := app.ManageMemory(ctx, "chart-owner-forget", memory.Mutation{Operation: "forget", Category: "preferences"})
	if err != nil || result["accepted"] != true {
		t.Fatal("real Forget rejected", result, err)
	}
	// Assert dependent behavior immediately after the real recovery/control action.
	if _, err = app.WebChartView(ctx, ref); err == nil {
		t.Fatal("Forget retained private Chart view")
	}
	if _, err = app.WebChartExport(ctx, ref, []int{0}); err == nil {
		t.Fatal("Forget retained original CSV data grant")
	}
	after, err := app.Plugins().Store.Status(ctx)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("control repaired selection", after, err)
	}
}
