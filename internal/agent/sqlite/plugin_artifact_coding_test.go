package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/llm"
	artifactcontract "github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Combine two real independently sealed producers through the ordinary owner
// prepare/verify/activate flow. Neither candidate supplies its expected answers.
func artifactCodingComposition(t *testing.T, s *Store, runs *Runs) (rsi.Host, string, rsi.Policy) {
	t.Helper()
	ctx := context.Background()
	host := installCodingPlugin(t, s, runs)
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, selected.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, _, artifactHost := artifactOwnerFixture(t)
	artifactSelected, err := artifactHost.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	artifactGraph, err := artifactHost.Store.Graph(ctx, artifactSelected.Revision)
	if err != nil || len(artifactGraph.Graph.Bindings) != 1 {
		t.Fatal(err)
	}
	binding := artifactGraph.Graph.Bindings[0]
	pkg, err := host.Install(ctx, artifactHost.Catalog.Path(binding.PackageDigest))
	if err != nil || pkg.Digest != binding.PackageDigest {
		t.Fatal("composition substituted the actual Artifact package", err)
	}
	graph.Graph.Bindings = append(graph.Graph.Bindings, binding)
	combined, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, "coding-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	artifactPolicy := artifactOwnerPolicy("artifact-code-owner")
	policy.ID = artifactPolicy.ID
	policy.AllowedRequests = append(policy.AllowedRequests, artifactPolicy.AllowedRequests...)
	policy.AllowedContracts = append(policy.AllowedContracts, artifactPolicy.AllowedContracts...)
	policy.Cases = append(policy.Cases, artifactPolicy.Cases...)
	acceptArtifactOwnerGraph(t, host, combined.Revision, policy)
	s.ConfigureArtifactHost(host.Catalog)
	return host, combined.Revision, policy
}

func TestArtifactWorkbenchActualCodingReportUsesIndependentCreationVersion(t *testing.T) {
	ctx := context.Background()
	s, runs, actor, provider, _ := codeFixture(t)
	host, firstGraph, policy := artifactCodingComposition(t, s, runs)
	provider.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "prepared original source"}, nil
	})
	mustSubmit(t, actor, "artifact-code", "Prepare original source")
	mustDrain(t, actor)
	var taskRef string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&taskRef); err != nil {
		t.Fatal(err)
	}
	original, err := s.SaveCodeReport(ctx, taskRef)
	if err != nil || original.MethodGraph != firstGraph || original.SemanticKind != "file" || original.MediaType != "application/json" {
		t.Fatal("actual coding report bypassed Artifact creation", original, err)
	}
	originalBytes := readArtifact(t, s, artifactOwner(t, s), original.Ref)
	if !json.Valid([]byte(originalBytes)) || strings.Contains(originalBytes, `"directory":`) || scalar(t, s, `SELECT count(*) FROM code_report_method_binding WHERE artifact_ref=? AND graph_revision=?`, original.Ref, firstGraph) != 1 {
		t.Fatal("report bytes or coding-method receipt changed", originalBytes)
	}
	graph, err := host.Store.Graph(ctx, firstGraph)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := host.Install(ctx, artifactChangedPackage(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "artifact-workbench" {
			graph.Graph.Bindings[i].PackageDigest = pkg.Digest
		}
	}
	updated, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "artifact-code-retention-upgrade"
	for i := range policy.Cases {
		if policy.Cases[i].Contract == artifactcontract.Recipe {
			policy.Cases[i].Expected = json.RawMessage(`{"retention_days":14,"media_kinds":["image","audio","video"],"overflow_name":"evolved-output.log","overflow_media":"text/x-log","stored_text":"Archived output.","publish_text":"Selected final artifacts."}`)
		}
	}
	acceptArtifactOwnerGraph(t, host, updated.Revision, policy)
	current, err := s.SaveCodeReport(ctx, taskRef)
	if err != nil || current.MethodGraph != updated.Revision || current.Name != "code-review.json" || current.MediaType != "application/json" || readArtifact(t, s, artifactOwner(t, s), current.Ref) != originalBytes {
		t.Fatal("new default replaced explicit coding metadata or original report bytes", current, err)
	}
	for ref, days := range map[string]int{original.Ref: 30, current.Ref: 14} {
		if scalar(t, s, `SELECT (expires_at-created_at)/86400000000 FROM artifact WHERE artifact_ref=?`, ref) != days {
			t.Fatal("coding report retention did not follow its independent accepted creation", ref)
		}
	}
	// Remove only Artifact. The Coding producer remains available; the expired
	// creation boundary cannot silently return to its original native defaults.
	onlyCoding := graph.Graph
	onlyCoding.Bindings = nil
	for _, binding := range graph.Graph.Bindings {
		if binding.InstanceID == "coding-workspace" {
			onlyCoding.Bindings = append(onlyCoding.Bindings, binding)
		}
	}
	if len(onlyCoding.Bindings) != 1 {
		t.Fatal("missing independent Coding binding")
	}
	removed, err := host.Prepare(ctx, onlyCoding)
	if err != nil {
		t.Fatal(err)
	}
	codingPolicy, err := host.Store.Policy(ctx, "coding-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	codingPolicy.ID = "artifact-code-remove-only-creation"
	acceptArtifactOwnerGraph(t, host, removed.Revision, codingPolicy)
	if _, err := s.CodeReport(ctx, taskRef, true); err != nil {
		t.Fatal("independent Coding read required Artifact", err)
	}
	before := scalar(t, s, `SELECT count(*) FROM artifact`)
	if _, err := s.SaveCodeReport(ctx, taskRef); !errors.Is(err, artifact.ErrUnavailable) || scalar(t, s, `SELECT count(*) FROM artifact`) != before {
		t.Fatal("removed creation silently used native policy", err)
	}
	if readArtifact(t, s, artifactOwner(t, s), original.Ref) != originalBytes {
		t.Fatal("plugin removal changed the original report reference")
	}
}

func TestArtifactWorkbenchCodingCommitKeepsBothMethodsBehindLateRevocation(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			s, runs, actor, provider, _ := codeFixture(t)
			host, graph, _ := artifactCodingComposition(t, s, runs)
			provider.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "prepared"}, nil
			})
			mustSubmit(t, actor, "artifact-code-revoke", "Prepare original source")
			mustDrain(t, actor)
			var ref string
			if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
				t.Fatal(err)
			}
			before := scalar(t, s, `SELECT count(*) FROM artifact`)
			agentID := idAgent(t, s)
			s.MainBoundary = func(point string) {
				if point != "before_code_report_commit" {
					return
				}
				var err error
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return runs.revokeMemory(ctx, tx, agentID) })
				case "stop":
					err = host.Store.Stop(ctx, true)
				case "quarantine":
					err = host.Store.Quarantine(ctx, graph)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.SaveCodeReport(ctx, ref)
			want := error(llm.ProviderUnavailable)
			if cause == "forget" {
				want = agent.ErrNotFound
			}
			if !errors.Is(err, want) || scalar(t, s, `SELECT count(*) FROM artifact`) != before || scalar(t, s, `SELECT count(*) FROM code_report_method_binding`) != 0 {
				t.Fatal("late revocation partially committed artifact or either method", err)
			}
		})
	}
}
