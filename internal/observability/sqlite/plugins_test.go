package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/z-chenhao/assistant/internal/rsi"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

func TestTraceLayoutCannotInventOrDropSourceRecords(t *testing.T) {
	tree := observability.ObservationTree{Items: []observability.Observation{{ID: "source", Status: "failed", Name: "authoritative"}}}
	good := contract.Layout{Nodes: []contract.Node{{ID: "source", Width: 4}}, Tabs: []string{"overview"}}
	if _, err := validateLayout(tree, good); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*contract.Layout){
		func(l *contract.Layout) { l.Nodes = nil }, func(l *contract.Layout) { l.Nodes[0].ID = "invented" },
		func(l *contract.Layout) { l.Nodes[0].Depth = 17 }, func(l *contract.Layout) { l.Nodes[0].X = 999 },
		func(l *contract.Layout) { l.Tabs = []string{"execute"} }, func(l *contract.Layout) { l.Tabs = []string{"overview", "overview"} },
	} {
		value := good
		value.Nodes = append([]contract.Node(nil), good.Nodes...)
		change(&value)
		if _, err := validateLayout(tree, value); err == nil {
			t.Fatal("invalid view accepted", value)
		}
	}
}

func TestTraceGuardianReaderDoesNotWaitForSourceSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`CREATE TABLE probe(value INTEGER);INSERT INTO probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for range 2 {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
	}
	var n int
	if err = s.guard.QueryRowContext(ctx, `SELECT value FROM probe`).Scan(&n); err != nil || n != 1 {
		t.Fatal("guardian waited for its own source snapshots", n, err)
	}
	if _, err = s.guard.ExecContext(ctx, `UPDATE probe SET value=2`); err == nil {
		t.Fatal("guardian reader gained writes")
	}
}

func TestFingerprintRetainsSQLiteNumericAndTextEquality(t *testing.T) {
	if valueFingerprint("integer", "1") != valueFingerprint("real", "1") || valueFingerprint("integer", "9007199254740993") == valueFingerprint("real", "9007199254740992") || valueFingerprint("text", "1") == valueFingerprint("integer", "1") {
		t.Fatal("stored-value comparison changed SQL equality")
	}
}

func TestTraceMetricProjectionBindsRecordedEvidenceAndKnownUsage(t *testing.T) {
	first := int64(2_000_000)
	rate := float64(50)
	tree := observability.ObservationTree{Items: []observability.Observation{{ID: "source", Status: "completed"}}, ProviderTimings: []observability.ProviderTiming{{Attempt: "a", Phase: "first_text", At: first}}, Generation: &observability.GenerationDetails{Metrics: observability.Metrics{KnownUsage: 1, Output: 100}}}
	good := contract.Layout{Timeline: &contract.Timeline{Start: 1_000_000, End: 4_000_000}, Nodes: []contract.Node{{ID: "source", Width: 4, Primary: true}}, Tabs: []string{"overview"}, FirstOutputAt: &first, OutputRate: &rate}
	if _, err := validateLayout(tree, good); err != nil {
		t.Fatal(err)
	}
	unknown := int64(3_000_000)
	bad := good
	bad.FirstOutputAt = &unknown
	if _, err := validateLayout(tree, bad); err == nil {
		t.Fatal("invented first-output accepted")
	}
	tree.Generation.Metrics.KnownUsage = 0
	if _, err := validateLayout(tree, good); err == nil {
		t.Fatal("unknown usage became a known rate")
	}
	good.OutputRate = nil
	if _, err := validateLayout(tree, good); err != nil {
		t.Fatal("unknown rate rejected", err)
	}
}

func TestTraceManagedHistoryWithoutCatalogCannotRestoreNativeDiffOrWorkbench(t *testing.T) {
	ctx := context.Background()
	old := map[string]any{"messages": []any{map[string]any{"role": "user", "text": "original"}}}
	next := map[string]any{"messages": []any{map[string]any{"role": "user", "text": "changed"}}}
	reader, db := contextFixture(t, old, next)
	query := observability.DiffQuery{RunID: "run", Step: 1, Section: "messages"}
	before, err := reader.ContextDiff(ctx, query)
	if err != nil || len(before.Items) != 1 {
		t.Fatal("never-managed source fixture lost native read", before, err)
	}
	// Persist prior activation evidence, then use the same unconfigured reader.
	// No current selection or Catalog is supplied to repair the business path.
	_, err = db.Exec(`CREATE TABLE rsi_activation(receipt_json TEXT);CREATE TABLE rsi_graph(revision TEXT,graph_json TEXT);CREATE TABLE rsi_package(digest TEXT,manifest_json TEXT);
 INSERT INTO rsi_activation VALUES('{"graph_revision":"original-graph"}');
 INSERT INTO rsi_graph VALUES('original-graph','{"bindings":[{"package_digest":"original-package"}]}');
 INSERT INTO rsi_package VALUES('original-package','{"provides":[{"contract":"steward.trace-layout/1"}]}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ContextDiff(ctx, query); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("missing Catalog restored native Diff after activation")
	}
	if _, err = reader.TraceWorkbenchSelection(ctx, ""); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("missing Catalog restored native workbench after activation")
	}
}

func TestTracePartialActivationSchemaIsNotNeverManaged(t *testing.T) {
	reader, db := contextFixture(t, map[string]any{}, map[string]any{})
	if _, err := db.Exec(`CREATE TABLE rsi_activation(receipt_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.TraceWorkbenchSelection(context.Background(), ""); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("partial activation schema restored native", err)
	}
}
