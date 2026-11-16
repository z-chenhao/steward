package rsi_test

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/observability"
	observabilitysqlite "github.com/z-chenhao/assistant/internal/observability/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"path/filepath"
	"testing"
)

func TestTraceColdMissingCatalogNeverRestoresNativeProjection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	receipt, err := f.actor.Submit(ctx, "trace-cold-catalog", "original retained execution")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), Trace: tracePackageVersion(t, "1.1.0", false)}); err != nil {
		t.Fatal(err)
	}
	selected, err := f.host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		expected, e := f.host.Store.Status(ctx)
		if e != nil {
			t.Fatal(e)
		}
		reader, e := observabilitysqlite.Open(filepath.Join(f.root, "state.db"))
		if e != nil {
			t.Fatal(e)
		}
		defer reader.Close()
		// This is the first business read on a fresh read-only pool. Do not select,
		// configure a Catalog or otherwise repair the retained activation first.
		if _, e = reader.TraceWorkbenchSelection(ctx, ""); !errors.Is(e, rsi.ErrNotFound) {
			t.Fatal("cold missing Catalog restored native workbench")
		}
		if _, e = reader.Traces(ctx, observability.TraceQuery{}); !errors.Is(e, rsi.ErrNotFound) {
			t.Fatal("cold missing Catalog restored native histogram")
		}
		if _, _, e = reader.ProjectedObservations(ctx, observability.ObservationQuery{RunID: receipt.RunID}); !errors.Is(e, rsi.ErrNotFound) {
			t.Fatal("cold missing Catalog restored native layout")
		}
		// Source facts remain independently inspectable; this is not a blanket ban.
		rounds, e := reader.Rounds(ctx, observability.RoundQuery{RunID: receipt.RunID, After: -1})
		if e != nil || len(rounds.Items) != 1 {
			t.Fatal("missing presentation hid authoritative source facts", rounds, e)
		}
		if after, e := f.host.Store.Status(ctx); e != nil || after != expected {
			t.Fatal("cold read changed the original persisted selection", after, e)
		}
	}
	check()
	if _, err = f.host.Store.Deactivate(ctx, "trace-catalog-disable", selected.Revision); err != nil {
		t.Fatal(err)
	}
	check()
}
