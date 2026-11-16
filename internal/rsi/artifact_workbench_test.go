package rsi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	artifact "github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// This is a different implementation of all four policies, not a native proxy
// or a version-only wrapper. Owner expectations below are independent literals.
func changedArtifactPackage(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	replacements := map[string]string{}
	for file, changes := range map[string][][2]string{
		"plugin.go": {{`Version: "1.0.0"`, `Version: "1.1.0"`}},
		"recipe.go": {{"days = 30", "days = 14"}, {"limit = 4096", "limit = 2048"}},
		"search.go": {{"h := selectionHeap{newest: true}", "h := selectionHeap{}"}, {"h := selectionHeap{}\n\tfor index := 0; index < request.Count; index++ {\n\t\tif err := ctx.Err(); err != nil {\n\t\t\treturn contract.Selection{}, err\n\t\t}\n\t\trow, err := contract.ReadCleanupRow(input)", "h := selectionHeap{newest: true}\n\tfor index := 0; index < request.Count; index++ {\n\t\tif err := ctx.Err(); err != nil {\n\t\t\treturn contract.Selection{}, err\n\t\t}\n\t\trow, err := contract.ReadCleanupRow(input)"}},
	} {
		source, err := filepath.Abs(filepath.Join("../plugin/artifactworkbench", file))
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
				t.Fatalf("variant mismatch in %s", file)
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
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(bundle, "plugin"), "../../cmd/plugins/artifact-workbench").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/artifact-workbench/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
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

func artifactOwnerCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "artifact", Contract: artifact.Recipe, Input: json.RawMessage(`{"retention_days":0}`), Expected: json.RawMessage(`{"retention_days":30,"media_kinds":["image","audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)},
		{InstanceID: "artifact", Contract: artifact.Read, Input: json.RawMessage(`{"media_type":"text/markdown","byte_size":21,"offset":0,"limit":0}`), Expected: json.RawMessage(`{"kind":"text","limit":4096}`)},
		{InstanceID: "artifact", Contract: artifact.Search, Input: json.RawMessage(`{"count":3,"query":" α ","limit":1}`), Inputs: []rsi.InputData{{ID: artifact.SearchInput, Data: artifact.EncodeSearch([]artifact.SearchRow{{CreatedAt: 30, Name: "Α report", MediaType: "text/plain"}, {CreatedAt: 50, Name: "other", MediaType: "application/json"}, {CreatedAt: 2, Name: "α older", MediaType: "text/plain"}})}}, Expected: json.RawMessage(`{"indices":[0],"limit":1}`)},
		{InstanceID: "artifact", Contract: artifact.Cleanup, Input: json.RawMessage(`{"count":3,"batch":2}`), Inputs: []rsi.InputData{{ID: artifact.CleanupInput, Data: artifact.EncodeCleanup([]artifact.CleanupRow{{ExpiresAt: 30}, {ExpiresAt: 2}, {ExpiresAt: 4}})}}, Expected: json.RawMessage(`{"indices":[1,2],"limit":2}`)},
	}
}

func artifactOwnerDecision(t *testing.T, f *fixture, graph rsi.ResolvedGraph, label string, cases []rsi.Case) (rsi.Activation, error) {
	t.Helper()
	ctx := context.Background()
	policy := rsi.Policy{ID: "artifact-policy-" + label, AllowedContracts: []string{artifact.Recipe, artifact.Read, artifact.Search, artifact.Cleanup}, AllowedRequests: []string{"input.read"}, Cases: cases}
	if err := f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	status, err := f.host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidate := rsi.Candidate{ID: "artifact-candidate-" + label, Baseline: status.Revision, Target: graph.Revision, PolicyID: policy.ID, Goal: "owner independently verifies artifact policies"}
	if err := f.host.Propose(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	decision, err := f.host.Verify(ctx, candidate.ID)
	return rsi.Activation{OperationID: "artifact-activate-" + label, Expected: status.Revision, Target: graph.Revision, DecisionID: decision}, err
}

func artifactGraph(t *testing.T, f *fixture, directory string) rsi.ResolvedGraph {
	t.Helper()
	pkg, err := f.host.Install(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := f.host.Prepare(context.Background(), rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "artifact", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "artifact", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

// Scope, raw bytes, actual Tool receipts and native Run compatibility belong to
// the owning adapters' regressions; these tests prove the isolated producer and
// independent control plane, and do not declare the whole feature migrated.
func TestArtifactWorkbenchIndependentPoliciesAndVersionLifecycle(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	first := artifactGraph(t, f, bootstrapPackage(t, "artifact-workbench", nil))
	second := artifactGraph(t, f, changedArtifactPackage(t))
	accept := func(graph rsi.ResolvedGraph, label string, cases []rsi.Case) {
		t.Helper()
		activation, err := artifactOwnerDecision(t, f, graph, label, cases)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.host.Activate(ctx, activation); err != nil {
			t.Fatal(err)
		}
	}
	accept(first, "original", artifactOwnerCases())
	before, err := f.host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifactOwnerDecision(t, f, second, "reject-changed", artifactOwnerCases()); !errors.Is(err, rsi.ErrVerification) {
		t.Fatal("candidate rewrote owner expectations", err)
	}
	if after, err := f.host.Store.Status(ctx); err != nil || after != before {
		t.Fatal("failed candidate changed selection", after, err)
	}
	cases := artifactOwnerCases()
	cases[0].Expected = json.RawMessage(`{"retention_days":14,"media_kinds":["image","audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)
	cases[1].Expected = json.RawMessage(`{"kind":"text","limit":2048}`)
	cases[2].Expected = json.RawMessage(`{"indices":[2],"limit":1}`)
	cases[3].Expected = json.RawMessage(`{"indices":[0,2],"limit":2}`)
	accept(second, "independent-update", cases)
	for _, version := range []struct {
		graph rsi.ResolvedGraph
		cases []rsi.Case
	}{{first, artifactOwnerCases()}, {second, cases}} {
		for _, c := range version.cases {
			raw, err := f.host.ReadService(ctx, version.graph.Revision, c.Contract, c.Input, c.Inputs)
			if err != nil {
				t.Fatal(c.Contract, err)
			}
			var got, want any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(c.Expected, &want); err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatalf("frozen %s: %s != %s", c.Contract, a, b)
			}
		}
	}
	accept(first, "owner-rollback", artifactOwnerCases())
	if err := f.host.Store.Quarantine(ctx, second.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.ReadService(ctx, second.Revision, artifact.Recipe, json.RawMessage(`{"retention_days":0}`), nil); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("quarantined policy still ran", err)
	}
	if _, err := f.host.ReadService(ctx, first.Revision, artifact.Recipe, json.RawMessage(`{"retention_days":0}`), nil); err != nil {
		t.Fatal("quarantine crossed graph boundary", err)
	}
	if _, err := f.host.Store.Deactivate(ctx, "artifact-owner-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.host.Store.Status(ctx)
	if err != nil || disabled.Revision != "" {
		t.Fatal(disabled, err)
	}
	reopened, err := agentsqlite.Open(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	catalog := rsi.NewCatalog(f.root)
	catalog.Launcher = fixtureExecutable
	cold := rsi.Host{Catalog: catalog, Store: reopened.Plugins()}
	if after, err := cold.Store.Status(ctx); err != nil || after != disabled {
		t.Fatal("cold control restored disabled selection", after, err)
	}
	if _, err := cold.ReadService(ctx, first.Revision, artifact.Recipe, json.RawMessage(`{"retention_days":0}`), nil); err != nil {
		t.Fatal("deactivation discarded retained graph", err)
	}
	var calls int
	if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call`).Scan(&calls); err != nil || calls != 0 {
		t.Fatal("pure owner policies wrote production calls", calls, err)
	}
}

func TestArtifactWorkbenchStreamFullCatalogueAndRejectPartialMetadata(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	graph := artifactGraph(t, f, bootstrapPackage(t, "artifact-workbench", nil))
	activation, err := artifactOwnerDecision(t, f, graph, "stream", artifactOwnerCases())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Activate(ctx, activation); err != nil {
		t.Fatal(err)
	}
	const count = 40000
	rows := make([]artifact.SearchRow, count)
	for i := range rows {
		rows[i] = artifact.SearchRow{CreatedAt: int64(i), Name: strings.Repeat("x", 220), MediaType: "text/plain"}
	}
	rows[0].Name = "needle old"
	rows[count-1].Name = "needle latest"
	data := artifact.EncodeSearch(rows)
	if len(data) <= rsiplugin.MaxInputBytes {
		t.Fatal("fixture no longer crosses ordinary input budget")
	}
	request := json.RawMessage(`{"count":40000,"query":"needle","limit":0}`)
	source := rsi.InputData{ID: artifact.SearchInput, Data: data}
	if _, err := f.host.ReadService(ctx, graph.Revision, artifact.Search, request, []rsi.InputData{source}); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("ordinary input budget widened", err)
	}
	raw, err := f.host.ReadServiceStream(ctx, graph.Revision, artifact.Search, request, source)
	if err != nil || string(raw) != `{"indices":[39999,0],"limit":50}` {
		t.Fatal("full catalogue truncated or misranked", string(raw), err)
	}
	for _, c := range []struct {
		name  string
		data  []byte
		count int
	}{
		{"truncated", data[:len(data)-1], count},
		{"trailing", append(append([]byte{}, data...), 0), count},
		{"wrong-count", data, count - 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			input, err := json.Marshal(artifact.SearchRequest{Count: c.count, Query: "needle"})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := f.host.ReadServiceStream(ctx, graph.Revision, artifact.Search, input, rsi.InputData{ID: artifact.SearchInput, Data: c.data})
			if err == nil || len(raw) != 0 {
				t.Fatal("partial metadata released a selection", string(raw), err)
			}
		})
	}
	store := f.store.Plugins()
	checks := 0
	store.Fence = func(context.Context, *sql.Tx) error {
		checks++
		if checks >= 5 {
			return rsi.ErrRevoked
		}
		return nil
	}
	fenced := rsi.Host{Store: store, Catalog: f.host.Catalog}
	if raw, err := fenced.ReadServiceStream(ctx, graph.Revision, artifact.Search, request, source); !errors.Is(err, rsi.ErrRevoked) || len(raw) != 0 {
		t.Fatal("revoked input exposed a result", string(raw), err)
	}
}
