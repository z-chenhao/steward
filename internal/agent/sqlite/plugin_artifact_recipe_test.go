package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	toolfake "github.com/z-chenhao/assistant/internal/tool/fake"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func artifactRecipeVersionPackage(t *testing.T) string { return artifactChangedPackage(t, "1.1.0") }

func artifactChangedPackage(t *testing.T, version string) string {
	t.Helper()
	directory := t.TempDir()
	replacements := map[string]string{}
	changesByFile := map[string][][2]string{"plugin.go": {{`Version: "1.0.0"`, `Version: "` + version + `"`}}}
	switch version {
	case "1.1.0":
		changesByFile["recipe.go"] = [][2]string{{"days = 30", "days = 14"}, {`"tool-output.txt"`, `"evolved-output.log"`}, {`"text/plain"`, `"text/x-log"`}, {`"Saved artifact."`, `"Archived output."`}, {`"Final artifacts selected."`, `"Selected final artifacts."`}}
	case "1.2.0":
		changesByFile["recipe.go"] = [][2]string{{`[]string{"image", "audio", "video"}`, `[]string{"audio", "video"}`}}
	case "1.3.0":
		changesByFile["search.go"] = [][2]string{{"h := selectionHeap{}", "h := selectionHeap{newest: true}"}}
	default:
		t.Fatal("unsupported real Artifact variant", version)
	}
	for file, changes := range changesByFile {
		source, err := filepath.Abs(filepath.Join("../../plugin/artifactworkbench", file))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		code := string(raw)
		for _, change := range changes {
			if strings.Count(code, change[0]) != 1 {
				t.Fatal("real recipe variant mismatch", file, change[0])
			}
			code = strings.Replace(code, change[0], change[1], 1)
		}
		override := filepath.Join(directory, file)
		if err := os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[source] = override
	}
	raw, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bundle := t.TempDir()
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(bundle, "plugin"), "../../../cmd/plugins/artifact-workbench").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/artifact-workbench/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	manifest, err = rsi.Seal(bundle, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func activateArtifactRecipeVersion(t *testing.T, host rsi.Host, directory, label string) string {
	policy := artifactOwnerPolicy(label)
	policy.Cases[0].Expected = json.RawMessage(`{"retention_days":14,"media_kinds":["image","audio","video"],"overflow_name":"evolved-output.log","overflow_media":"text/x-log","stored_text":"Archived output.","publish_text":"Selected final artifacts."}`)
	return activateArtifactPolicyPackage(t, host, directory, policy)
}

func activateArtifactPolicyPackage(t *testing.T, host rsi.Host, directory string, policy rsi.Policy) string {
	t.Helper()
	ctx := context.Background()
	pkg, err := host.Install(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "artifact-workbench", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "artifact-workbench", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	acceptArtifactOwnerGraph(t, host, graph.Revision, policy)
	return graph.Revision
}

func acceptArtifactOwnerGraph(t *testing.T, host rsi.Host, target string, policy rsi.Policy) {
	t.Helper()
	ctx := context.Background()
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: policy.ID, Baseline: status.Revision, Target: target, PolicyID: policy.ID, Goal: "owner independent Artifact behavior"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: policy.ID, Expected: status.Revision, Target: target, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactWorkbenchOutputRecipeRealUpgradeFrozenRunAndExplicitRetention(t *testing.T) {
	ctx := context.Background()
	newPackage := artifactRecipeVersionPackage(t)
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "plugin-default", true: "explicit-native-default"}[explicit], func(t *testing.T) {
			calls := []llm.ToolCall{{ID: "large", Name: "mcp__fixture__large", Arguments: `{}`}}
			var host rsi.Host
			store, runs, oldClaim, oldExecutor, oldExchange := admitPolicyRun(t, toolfake.Artifacts(), calls, false, func(store *Store, runs *Runs) {
				host = installArtifactWorkbench(t, store, runs)
				if explicit {
					days := 30
					store.Artifacts().RetentionDays = &days
				}
			})
			defer runs.Release(ctx, oldClaim)
			activateArtifactRecipeVersion(t, host, newPackage, "artifact-evolved-recipe")
			old, err := oldExecutor.Execute(ctx, oldExchange, calls)
			if err != nil || len(old) != 1 || old[0].Error != nil || old[0].Text != "Saved artifact." || len(old[0].ArtifactRefs) != 1 {
				t.Fatal("old Run adopted a new output recipe", old, err)
			}
			assertArtifactRecipeOutput(t, store, old[0].ArtifactRefs[0], "tool-output.txt", "text/plain", 30)
			// End this fixture's first Main normally through its terminal API,
			// then submit a distinct request. Reusing the original request key
			// must never masquerade as a new frozen graph.
			oldRun, err := runs.Load(ctx, oldClaim)
			if err != nil {
				t.Fatal(err)
			}
			if err := runs.Fail(ctx, oldClaim, oldRun.Version, "PROVIDER_UNAVAILABLE"); err != nil {
				t.Fatal(err)
			}
			receipt, err := runs.Submit(ctx, "artifact-new-input", conversation.Text("produce output with the selected recipe"))
			if err != nil {
				t.Fatal(err)
			}
			runID := receipt.RunID
			claim, err := runs.Claim(ctx, runID)
			if err != nil {
				t.Fatal(err)
			}
			defer runs.Release(ctx, claim)
			if err := runs.Initialize(ctx, claim); err != nil {
				t.Fatal(err)
			}
			exchange := artifactCompatibilityExchange(t, store, runs, claim, calls)
			executor := tool.Executor{Registry: runs.registry, Store: runs.ToolStore(claim), Concurrency: 1}
			next, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(next) != 1 || next[0].Error != nil || next[0].Text != "Archived output." || len(next[0].ArtifactRefs) != 1 {
				t.Fatal("ordinary output did not use independent recipe", next, err)
			}
			days := 14
			if explicit {
				days = 30
			}
			assertArtifactRecipeOutput(t, store, next[0].ArtifactRefs[0], "evolved-output.log", "text/x-log", days)
			if scalar(t, store, `SELECT count(*) FROM run_artifact_recipe`) != 2 {
				t.Fatal("recipe not accepted once per actual Run")
			}
			if _, err := store.db.Exec(`UPDATE run_artifact_recipe SET retention_days=1`); err == nil {
				t.Fatal("accepted output recipe mutable")
			}
			if _, err := store.db.Exec(`DELETE FROM run_artifact_recipe`); err == nil {
				t.Fatal("accepted output recipe discarded")
			}
			// Current operator settings cannot rewrite an accepted Run recipe.
			changed := 2
			store.Artifacts().RetentionDays = &changed
			if err := runs.prepareArtifactRecipe(ctx, claim); err != nil {
				t.Fatal(err)
			}
			if scalar(t, store, `SELECT retention_days FROM run_artifact_recipe WHERE run_id=?`, runID) != days {
				t.Fatal("config change rewrote accepted creation choice")
			}
		})
	}
}

func assertArtifactRecipeOutput(t *testing.T, store *Store, ref, name, media string, days int) {
	t.Helper()
	var actualName, actualMedia string
	var ttl int64
	if err := store.db.QueryRow(`SELECT name,media_type,expires_at-created_at FROM artifact WHERE artifact_ref=?`, ref).Scan(&actualName, &actualMedia, &ttl); err != nil {
		t.Fatal(err)
	}
	if actualName != name || actualMedia != media || ttl != int64(days)*int64((24*time.Hour).Microseconds()) {
		t.Fatal("accepted metadata/retention not applied", actualName, actualMedia, ttl)
	}
	if !strings.HasPrefix(readArtifact(t, store, artifactOwner(t, store), ref), "LARGE-FIXTURE-") {
		t.Fatal("producer changed original output bytes")
	}
}

func TestArtifactWorkbenchEffectPreparesBeforeDispatchAndReconcilesWithoutCandidate(t *testing.T) {
	ctx := context.Background()
	invoked := atomic.Int32{}
	queries := atomic.Int32{}
	var store *Store
	var host rsi.Host
	definition := tool.Definition{Spec: tool.Spec{Name: "mcp__fixture__artifact_effect", Version: 1, InputSchema: `{"type":"object","additionalProperties":false}`, Permission: "external_effect", ReplayPolicy: "query_before_retry", Risk: "high", TimeoutMS: 2000, OutputBytes: 1024, Provider: "fixture", ConflictPolicy: "none"},
		Invoke: func(context.Context, tool.Request) tool.Outcome {
			invoked.Add(1)
			if scalar(t, store, `SELECT count(*) FROM run_artifact_recipe`) != 1 {
				t.Error("effect ran without a durable recipe")
			}
			return tool.Outcome{Ambiguous: true}
		},
		Query: func(context.Context, tool.Request) tool.Evidence {
			queries.Add(1)
			return tool.Evidence{State: "succeeded", Result: tool.Result{Output: &tool.BinaryOutput{Name: "explicit.bin", MediaType: "application/octet-stream", Reader: strings.NewReader("authoritative original bytes")}}}
		},
	}
	calls := []llm.ToolCall{{ID: "effect", Name: definition.Spec.Name, Arguments: `{}`}}
	var runs *Runs
	var claim agent.RunClaim
	var executor tool.Executor
	var exchange string
	store, runs, claim, executor, exchange = admitPolicyRun(t, []tool.Definition{definition}, calls, false, func(store *Store, runs *Runs) { host = installArtifactWorkbench(t, store, runs) })
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	if scalar(t, store, `SELECT count(*) FROM run_artifact_recipe`) != 1 || invoked.Load() != 0 {
		t.Fatal("recipe not frozen before approval/effect")
	}
	approvePolicy(t, store, runs)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || invoked.Load() != 1 {
		t.Fatal(results, err)
	}
	run, err := runs.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.Fail(ctx, claim, run.Version, "UNKNOWN_SIDE_EFFECT"); err != nil {
		t.Fatal(err)
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if err := runs.ReconcileUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	observations, err := runs.ToolOutcomes(ctx)
	if err != nil || len(observations) != 1 || observations[0].Result.Error != nil || len(observations[0].Result.ArtifactRefs) != 1 {
		t.Fatal("independent receipt query depended on current candidate", observations, err)
	}
	ref := observations[0].Result.ArtifactRefs[0]
	descriptor, err := store.Artifacts().Describe(ctx, artifactOwner(t, store), ref)
	if err != nil || descriptor.Name != "explicit.bin" || descriptor.MediaType != "application/octet-stream" || readArtifact(t, store, artifactOwner(t, store), ref) != "authoritative original bytes" {
		t.Fatal("explicit receipt data rewritten", descriptor, err)
	}
	if err := runs.ReconcileUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	if invoked.Load() != 1 || queries.Load() != 1 || scalar(t, store, `SELECT attempt_count FROM tool_invocation`) != 1 {
		t.Fatal("unknown effect or authoritative receipt replayed")
	}
}

func TestArtifactWorkbenchChildInheritsFrozenCreationRequest(t *testing.T) {
	store, runs, actor, provider := childFixture(t)
	installArtifactWorkbench(t, store, runs)
	days := 30
	store.Artifacts().RetentionDays = &days
	changed := atomic.Bool{}
	provider.respond = func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(request) && changed.CompareAndSwap(false, true) {
			// Only subsequent admission settings change in this fixture. Child
			// creation must copy its parent's original request, not reread them.
			later := 2
			store.Artifacts().RetentionDays = &later
		}
		return childPolicy(request)
	}
	mustSubmit(t, actor, "artifact-request-inheritance", "perform delegated work")
	mustDrain(t, actor)
	if scalar(t, store, `SELECT count(*) FROM run r JOIN run_artifact_request q USING(run_id) WHERE r.kind='worker' AND q.retention_days=30`) != 3 {
		t.Fatal("Root/Child/grandchild lost explicit creation choice")
	}
	if scalar(t, store, `SELECT count(*) FROM run c JOIN run_artifact_request cq ON cq.run_id=c.run_id JOIN run_artifact_request pq ON pq.run_id=c.parent_run_id WHERE c.parent_run_id IS NOT NULL AND cq.retention_days!=pq.retention_days`) != 0 {
		t.Fatal("child reread changed process configuration")
	}
}

func TestV68ArtifactRecipeMigrationPreservesOriginalFactsAndNativeBoundary(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db, migrations[:68]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES('original','agent','conversation','main','queued','cap','model','{"messages":[{"text":"frozen original request"}]}',0,1,1,1);
 INSERT INTO rsi_run VALUES('original',NULL,'[]');
 INSERT INTO artifact(artifact_id,artifact_ref,owner_user_id,owner_agent_id,run_id,media_type,name,byte_size,content_hash,storage_key,visibility,retention_class,created_at,expires_at,put_key) VALUES('old','artifact:old','local','agent','original','text/plain','original.txt',3,'original-hash','original-key','run','standard',10,100,'original-put');`); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT json_object('checkpoint',r.checkpoint_json,'services',b.managed_services_json,'graph',b.graph_revision,'name',a.name,'hash',a.content_hash,'expires',a.expires_at,'version',a.version) FROM run r JOIN rsi_run b USING(run_id) JOIN artifact a USING(run_id)`
	var before, after string
	if err := db.QueryRow(snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("old Run/reference facts rewritten", before, after, err)
	}
	var requests, recipes int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM run_artifact_request),(SELECT count(*) FROM run_artifact_recipe)`).Scan(&requests, &recipes); err != nil || requests != 0 || recipes != 0 {
		t.Fatal("migration backfilled a new business choice", requests, recipes, err)
	}
}

func TestArtifactWorkbenchUnavailableRecipeRejectsOverflowAndKeepsInlineControl(t *testing.T) {
	ctx := context.Background()
	definitions := []tool.Definition{
		{Spec: tool.Spec{Name: "mcp__fixture__inline", Version: 1, Permission: "read_only", ReplayPolicy: "safe", OutputBytes: 1024, InputSchema: `{"type":"object","additionalProperties":false}`}, Execute: func(context.Context, string) tool.Result { return tool.Result{Text: "bounded inline result"} }},
		{Spec: tool.Spec{Name: "mcp__fixture__overflow", Version: 1, Permission: "read_only", ReplayPolicy: "safe", OutputBytes: 1024, InputSchema: `{"type":"object","additionalProperties":false}`}, Execute: func(context.Context, string) tool.Result {
			return tool.Result{Text: strings.Repeat("large original output", 1024)}
		}},
	}
	calls := []llm.ToolCall{{ID: "inline", Name: definitions[0].Spec.Name, Arguments: `{}`}, {ID: "overflow", Name: definitions[1].Spec.Name, Arguments: `{}`}}
	store, runs, claim, executor, exchange := admitPolicyRun(t, definitions, calls, false, func(store *Store, runs *Runs) {
		host := installArtifactWorkbench(t, store, runs)
		status, err := host.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.Store.Deactivate(ctx, "disable-artifact-output", status.Revision); err != nil {
			t.Fatal(err)
		}
	})
	defer runs.Release(ctx, claim)
	executor.Concurrency = 1
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	if results[0].Error != nil || results[0].Text != "bounded inline result" {
		t.Fatal("independent inline control required Artifact", results[0])
	}
	if results[1].Error == nil || results[1].Error.Code != "PROVIDER_UNAVAILABLE" || results[1].Text != "" || len(results[1].ArtifactRefs) != 0 {
		t.Fatal("unavailable output used native or leaked partial content", results[1])
	}
	if scalar(t, store, `SELECT count(*) FROM artifact`) != 0 || scalar(t, store, `SELECT count(*) FROM run_artifact_recipe`) != 0 {
		t.Fatal("unavailable method created new artifact facts")
	}
}
