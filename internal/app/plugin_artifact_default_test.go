package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/config"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
)

func TestArtifactDefaultProductionOwnerWithoutModelAndColdLifecycle(t *testing.T) {
	carrier := os.Getenv("STEWARD_TEST_APP_DEFAULT_BUNDLE")
	if carrier == "" {
		t.Skip("requires actual complete30 distribution;artifact-plugins gate supplies it")
	}
	carrier, err := filepath.Abs(carrier)
	if err != nil {
		t.Fatal(err)
	}
	directories, err := shippedPluginDirectories(carrier)
	if err != nil || directories.Artifact == "" {
		t.Fatal("complete Artifact distribution missing", err)
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretmemory.New()
	openControl := func() *App {
		t.Helper()
		a, err := open(ctx, cfg, "", secrets, true)
		if err != nil {
			t.Fatal(err)
		}
		host := a.Plugins()
		host.Catalog.Launcher = carrier
		a.store.ConfigureArtifactHost(host.Catalog)
		// Inject only the trusted executable location. The App, source config,
		// full owner policy, actual subprocesses and raw store are production.
		a.prepareArtifactPlugins = func(ctx context.Context) error {
			web, err := prepareWebDefault(ctx, host, cfg, directories.Extract)
			if err != nil {
				return err
			}
			return builtin.EnsureDefault(ctx, host, directories, web)
		}
		return a
	}
	a := openControl()
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	descriptor, err := a.SaveArtifact(ctx, artifact.Input{Name: "owner.txt", MediaType: "text/plain"}, strings.NewReader("original owner bytes"))
	if err != nil || descriptor.SemanticKind != "file" || descriptor.MethodGraph == "" {
		t.Fatal("ordinary owner save did not dispatch managed recipe", descriptor, err)
	}
	selected, err := a.Plugins().Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := a.Plugins().Store.Graph(ctx, selected.Revision)
	if err != nil || len(graph.Graph.Bindings) != 30 {
		t.Fatal("owner used a partial distribution", err)
	}
	var policyJSON string
	if err := a.store.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT p.policy_json FROM rsi_policy p JOIN rsi_candidate c ON c.policy_id=p.policy_id WHERE c.target=?`, selected.Revision).Scan(&policyJSON)
	}); err != nil {
		t.Fatal(err)
	}
	var policy rsi.Policy
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil || len(policy.Cases) != 243 {
		t.Fatal("owner bootstrap omitted independent cases", len(policy.Cases), err)
	}
	// No model, Skills or runtime execution has been required for creation.
	diagnostics, err := a.Diagnostics(ctx)
	if err != nil || diagnostics.ActiveModelProfileID != nil {
		t.Fatal("owner save initialized model", diagnostics, err)
	}
	if _, err := a.CleanupArtifacts(ctx, true); err != nil {
		t.Fatal("ordinary owner cleanup unavailable", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a = openControl()
	read, err := a.OpenArtifact(ctx, descriptor.Ref)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(read)
	read.Close()
	if err != nil || string(body) != "original owner bytes" {
		t.Fatal("cold raw bytes changed", err)
	}
	cold, err := a.DescribeArtifact(ctx, descriptor.Ref)
	if err != nil || cold.MethodGraph != descriptor.MethodGraph || cold.SemanticKind != "file" {
		t.Fatal("cold semantic fact changed", cold, err)
	}
	if _, err := a.Plugins().Store.Deactivate(ctx, "owner-disable-artifact", selected.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a = openControl()
	if _, err := a.SaveArtifact(ctx, artifact.Input{Name: "blocked.txt", MediaType: "text/plain"}, strings.NewReader("blocked")); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("cold disabled owner restored native", err)
	}
	if _, err := a.CleanupArtifacts(ctx, false); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("disabled cleanup restored native", err)
	}
	if err := a.DeleteArtifact(ctx, descriptor.Ref, descriptor.Version); err != nil {
		t.Fatal("independent owner deletion required plugin", err)
	}
	public, err := json.Marshal(cold)
	if err != nil || strings.Contains(string(public), "semantic_kind") || strings.Contains(string(public), "method_graph") {
		t.Fatal("private implementation fact exposed", string(public), err)
	}
}

func TestArtifactFreshUnbundledProductionOwnerCannotUseNative(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := open(ctx, cfg, "", secretmemory.New(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// The Go test executable has no shipped plugin directory. This is a fresh
	// ordinary product App, not an explicitly native SDK fixture.
	if _, err := a.SaveArtifact(ctx, artifact.Input{Name: "no-fallback.txt", MediaType: "text/plain"}, strings.NewReader("new bytes")); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("unbundled production admitted native creation", err)
	}
}
