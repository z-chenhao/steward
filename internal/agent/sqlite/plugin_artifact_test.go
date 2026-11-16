package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func installArtifactWorkbench(t *testing.T, store *Store, runs *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, store, runs, "artifact-workbench", artifactOwnerPolicy("artifact-workbench-owner"))
}

func artifactOwnerPolicy(id string) rsi.Policy {
	return rsi.Policy{
		ID: id, AllowedRequests: []string{"input.read"},
		AllowedContracts: []string{contract.Recipe, contract.Read, contract.Search, contract.Cleanup},
		Cases: []rsi.Case{
			{InstanceID: "artifact-workbench", Contract: contract.Recipe, Input: json.RawMessage(`{"retention_days":0}`), Expected: json.RawMessage(`{"retention_days":30,"media_kinds":["image","audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)},
			{InstanceID: "artifact-workbench", Contract: contract.Read, Input: json.RawMessage(`{"media_type":"text/markdown","byte_size":21,"offset":0,"limit":0}`), Expected: json.RawMessage(`{"kind":"text","limit":4096}`)},
			{InstanceID: "artifact-workbench", Contract: contract.Search, Input: json.RawMessage(`{"count":3,"query":" α ","limit":1}`), Inputs: []rsi.InputData{{ID: contract.SearchInput, Data: contract.EncodeSearch([]contract.SearchRow{{CreatedAt: 30, Name: "Α report", MediaType: "text/plain"}, {CreatedAt: 50, Name: "other", MediaType: "application/json"}, {CreatedAt: 2, Name: "α older", MediaType: "text/plain"}})}}, Expected: json.RawMessage(`{"indices":[0],"limit":1}`)},
			{InstanceID: "artifact-workbench", Contract: contract.Cleanup, Input: json.RawMessage(`{"count":3,"batch":2}`), Inputs: []rsi.InputData{{ID: contract.CleanupInput, Data: contract.EncodeCleanup([]contract.CleanupRow{{ExpiresAt: 30}, {ExpiresAt: 2}, {ExpiresAt: 4}})}}, Expected: json.RawMessage(`{"indices":[1,2],"limit":2}`)},
		},
	}
}

func TestArtifactWorkbenchSearchNeverFallsBackAfterOwnerDeactivation(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "search", Name: artifacttools.Search, Arguments: `{}`}}
	var historicalRef string
	store, runs, claim, executor, exchange := admitPolicyRun(t, artifacttools.Definitions(), calls, false, func(store *Store, runs *Runs) {
		historicalRef = putArtifact(t, store, artifactOwner(t, store), "pre-activation original input")
		host := installArtifactWorkbench(t, store, runs)
		status, err := host.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.Store.Deactivate(ctx, "owner-disable-artifact", status.Revision); err != nil {
			t.Fatal(err)
		}
	})
	defer runs.Release(ctx, claim)
	attachHistoricalArtifactFixture(t, store, claim.RunID(), historicalRef)
	if scalar(t, store, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?`, claim.RunID(), contract.Search) != 1 {
		t.Fatal("new disabled Run forgot migration boundary")
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("disabled producer fell back to native search", results, err)
	}
}

func TestArtifactWorkbenchSearchDoesNotReserveWriter(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "search", Name: artifacttools.Search, Arguments: `{}`}}
	var historicalRef string
	store, runs, claim, _, exchange := admitPolicyRun(t, artifacttools.Definitions(), calls, false, func(store *Store, runs *Runs) {
		historicalRef = putArtifact(t, store, artifactOwner(t, store), "pre-activation original input")
		installArtifactWorkbench(t, store, runs)
	})
	defer runs.Release(ctx, claim)
	attachHistoricalArtifactFixture(t, store, claim.RunID(), historicalRef)
	tools := runs.ToolStore(claim).(*tools)
	invs, err := tools.Accept(ctx, exchange, calls, runs.registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	inv, err := tools.Prepare(ctx, invs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tools.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(ctx, filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := tools.ExecuteBuiltin(readCtx, inv)
	if err != nil || result.Error != nil || !strings.Contains(result.Text, "input.txt") {
		t.Fatal("pure Artifact search reserved the WAL writer", result, err)
	}
}

// Existing input references do not need a new creation policy to be searched.
// This synthetic fixture grants an original native Artifact to the new Run; it
// is not evidence of an archived old-version Run/Child migration.
func attachHistoricalArtifactFixture(t *testing.T, store *Store, runID, ref string) {
	t.Helper()
	owner := artifactOwner(t, store)
	if err := store.Transaction(context.Background(), func(tx *sql.Tx) error {
		return store.Artifacts().PinTx(context.Background(), tx, owner, ref, "run", runID)
	}); err != nil {
		t.Fatal(err)
	}
}
