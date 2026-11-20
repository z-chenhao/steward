package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
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

func TestArtifactWorkbenchSearchTenThousandAuthorizedRows(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "one", Name: artifacttools.Search, Arguments: `{"query":"needle","limit":1}`}}
	store, runs, claim, executor, exchange := admitPolicyRun(t, artifacttools.Definitions(), calls, false, func(s *Store, r *Runs) { installArtifactWorkbench(t, s, r) })
	defer runs.Release(ctx, claim)
	defer keepRunClaim(runs, claim)()
	const count = 10000
	now := time.Now()
	_, err := store.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
	INSERT INTO artifact(artifact_id,artifact_ref,owner_user_id,owner_agent_id,run_id,media_type,name,byte_size,content_hash,storage_key,visibility,retention_class,created_at,expires_at)
	SELECT printf('%064x',i),'artifact:'||printf('%064x',i),?,agent_id,run_id,'text/plain',CASE i WHEN ? THEN 'needle' ELSE 'other' END,1,printf('%064x',i),printf('%064x',i),'run','standard',?,? FROM n CROSS JOIN run WHERE run_id=?`, count, artifact.LocalUser, count, now.UnixMicro(), now.Add(time.Hour).UnixMicro(), claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(result) != 1 || result[0].Error != nil {
		t.Fatal(result, err)
	}
	var selected []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(result[0].Text), &selected); err != nil || len(selected) != 1 || selected[0].Name != "needle" {
		t.Fatal(result, err)
	}
}

func TestArtifactWorkbenchSearchContinuesPastFiftyTiedFiles(t *testing.T) {
	ctx := context.Background()
	first := []llm.ToolCall{{ID: "first", Name: artifacttools.Search, Arguments: `{"query":"same","limit":50,"cursor":""}`}}
	s, r, claim, exec, exchange := admitPolicyRun(t, artifacttools.Definitions(), first, false, func(s *Store, r *Runs) { installArtifactWorkbench(t, s, r) })
	defer keepRunClaim(r, claim)()
	now := time.Now()
	_, err := s.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<51)
 INSERT INTO artifact(artifact_id,artifact_ref,owner_user_id,owner_agent_id,run_id,media_type,name,byte_size,content_hash,storage_key,visibility,retention_class,created_at,expires_at)
 SELECT printf('%064x',i),'artifact:'||printf('%064x',i),?,agent_id,run_id,'text/plain','same',1,printf('%064x',i),printf('%064x',i),'run','standard',?,? FROM n CROSS JOIN run WHERE run_id=?`, artifact.LocalUser, now.UnixMicro(), now.Add(time.Hour).UnixMicro(), claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Execute(ctx, exchange, first)
	var page artifactSearchPage
	if err != nil || len(output) != 1 || output[0].Error != nil || json.Unmarshal([]byte(output[0].Text), &page) != nil || len(page.Artifacts) != 50 || page.NextCursor == "" {
		t.Fatal(output, err)
	}
	cursor := page.NextCursor
	ids := map[string]bool{}
	for i, d := range page.Artifacts {
		if d.Ref != fmt.Sprintf("artifact:%064x", i+1) {
			t.Fatalf("unexpected item %d: %s", i, d.Ref)
		}
		ids[d.Ref] = true
	}
	cont := []llm.ToolCall{{ID: "final", Name: artifacttools.Search, Arguments: fmt.Sprintf(`{"query":"same","limit":1,"cursor":%q}`, cursor)}, {ID: "changed-query", Name: artifacttools.Search, Arguments: fmt.Sprintf(`{"query":"different","cursor":%q}`, cursor)}}
	exchange = artifactCompatibilityExchange(t, s, r, claim, cont)
	out, err := exec.Execute(ctx, exchange, cont)
	var last artifactSearchPage
	if err != nil || len(out) != 2 || out[0].Error != nil || json.Unmarshal([]byte(out[0].Text), &last) != nil || len(last.Artifacts) != 1 || last.NextCursor != "" || ids[last.Artifacts[0].Ref] || last.Artifacts[0].Ref != fmt.Sprintf("artifact:%064x", 51) || out[1].Error == nil || out[1].Error.Code != "INVALID_ARGUMENT" {
		t.Fatal(out, err)
	}
}
