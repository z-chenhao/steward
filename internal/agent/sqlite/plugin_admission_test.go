package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestPluginSnapshotWithoutCatalogKeepsContextServicesManaged(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := computerClaims(t)
	installRuntimeFeature(t, s, r, "docx-reader", rsi.Policy{ID: "context-admission-owner", AllowedContracts: []string{"steward.docx-text/1"}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "docx-reader", Contract: "steward.docx-text/1", Input: json.RawMessage(`{"input_id":"document"}`), Inputs: []rsi.InputData{{ID: "document", Data: []byte("not a DOCX")}}, Expected: json.RawMessage(`{"error_code":"INVALID_ARGUMENT"}`)},
	}})
	r.plugins = nil
	b := newComputerPluginExecution(t, s, r)
	managed, err := r.managedRunService(ctx, b.req.RunID, "steward.docx-text/1")
	if err != nil || !managed || scalar(t, s, `SELECT count(*) FROM run_context_policy WHERE run_id=? AND json_extract(policy_json,'$.docx_plugin')=1`, b.req.RunID) != 1 {
		t.Fatal("Context used absent catalog as a native fallback", managed, err)
	}
}

func TestPluginSnapshotWithoutCatalogKeepsPersistedManagedBoundary(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerClaims(t)
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision IS NULL AND managed_services_json='[]'`, bridges[0].req.RunID) != 1 {
		t.Fatal("unbundled native admission did not freeze explicit boundary")
	}
	host := installComputerPlugin(t, s, r)
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	b := newComputerPluginExecution(t, s, r)
	managed, err := r.managedRunService(ctx, b.req.RunID, contract.DocumentationContract)
	if err != nil || !managed || scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=?`, b.req.RunID, selected.Revision) != 1 {
		t.Fatal("missing executable catalog changed persisted activation", managed, err)
	}
	p := &surfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	result, err := computerPluginCall(t, b, "missing-catalog", "bind", "", "getApp", "notes")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.ensures != 0 {
		t.Fatal("catalog absence restored native or acquired Computer", result, err, p.ensures)
	}
}

func TestV62NativePluginBindingMigrationPreservesOriginalRequestsAndReceipts(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerClaims(t)
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	computerDocumentationText(t, b, "native-history", token, "documentation")
	if err := b.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	host := installComputerPlugin(t, s, r)
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	managed := newComputerPluginExecution(t, s, r)
	snapshot := func(store *Store) string {
		t.Helper()
		values := []string{}
		for _, query := range []string{
			`SELECT coalesce(group_concat(value,'|'),'') FROM (SELECT checkpoint_json||':'||version AS value FROM run ORDER BY run_id)`,
			`SELECT coalesce(group_concat(value,'|'),'') FROM (SELECT policy_json AS value FROM run_context_policy ORDER BY run_id)`,
			`SELECT coalesce(group_concat(value,'|'),'') FROM (SELECT request_json AS value FROM model_generation ORDER BY generation_id)`,
			`SELECT coalesce(group_concat(value,'|'),'') FROM (SELECT coalesce(result_json,'null') AS value FROM tool_invocation ORDER BY invocation_id)`,
			`SELECT coalesce(group_concat(value,'|'),'') FROM (SELECT request_json AS value FROM code_manifest ORDER BY execution_id)`,
		} {
			var value string
			if err := store.db.QueryRow(query).Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		data, _ := json.Marshal(values)
		return string(data)
	}
	before := snapshot(s)
	// Reproduce the actual schema62 carrier: absence arose at admission, not
	// from an allowed delete. Restore its immutable trigger before reopening.
	if _, err = s.db.Exec(removeComputerActionFixtureSchema+`DROP TABLE task_label_plan; DROP TRIGGER rsi_run_immutable_delete;DELETE FROM rsi_run WHERE run_id=?;CREATE TRIGGER rsi_run_immutable_delete BEFORE DELETE ON rsi_run BEGIN SELECT RAISE(ABORT,'immutable plugin binding');END;PRAGMA user_version=62`, b.req.RunID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('run_artifact_request','run_artifact_recipe')`) != 0 {
		t.Fatal("schema62 fixture retained later Artifact protocol tables")
	}
	root := filepath.Dir(s.path)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if snapshot(next) != before {
		t.Fatal("migration rewrote frozen context/request/receipt/manifest")
	}
	if scalar(t, next, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision IS NULL AND managed_services_json='[]'`, b.req.RunID) != 1 || scalar(t, next, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=? AND managed_services_json LIKE '%steward.computer-documentation/1%'`, managed.req.RunID, selected.Revision) != 1 {
		t.Fatal("migration changed old native or managed boundary")
	}
	for _, id := range []string{b.req.RunID, managed.req.RunID} {
		if _, err = next.db.Exec(`DELETE FROM rsi_run WHERE run_id=?`, id); err == nil {
			t.Fatal("migration lost immutable binding", id)
		}
	}
}
