package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/internal/tool/mcp"
)

func TestMCPPluginNamespaceActualDiscoveryRetainedSourceUpgradeRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	source := "  " + strings.Repeat("界", 700) + "\nsecond line is not the namespace summary"
	defs, _ := mcpPluginDefinitions(t, source)
	s, r, a, p := childFixture(t, append(defs, codemode.Definition(), tool.SearchDefinition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "namespace observed"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `return (await describeNamespace("managed")).description;`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Describe the allowed tool namespace")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id, want string, managed bool) {
		t.Helper()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var specs []tool.Spec
		if json.Unmarshal([]byte(raw), &specs) != nil {
			t.Fatal(raw)
		}
		count := 0
		for _, spec := range specs {
			if strings.HasPrefix(spec.Name, "mcp__managed__") {
				count++
				current, _ := r.registry.Resolve(spec.Name)
				if spec.NamespaceDescription != want || spec.ResourceHash != current.Spec.ResourceHash {
					t.Fatal("namespace policy rewrote original scope or frozen result", spec.NamespaceDescription, len(want))
				}
			}
		}
		if count != 2 {
			t.Fatal("lost the original namespace tools", count)
		}
		selected, err := r.managedRunService(ctx, id, contract.Namespace)
		if err != nil || selected != managed {
			t.Fatal("wrong immutable namespace boundary", selected, managed, err)
		}
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, codemode.ToolName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got tool.Result
		var output []struct {
			Value string `json:"value"`
		}
		if json.Unmarshal([]byte(raw), &got) != nil || got.Error != nil || json.Unmarshal([]byte(got.Text), &output) != nil || len(output) != 1 || output[0].Value != want {
			t.Fatal("real CodeMode discovery did not use the original frozen namespace", raw, len(want))
		}
	}
	legacy := queue("mcp-namespace-native")
	oldGraph, _ := selectMCPVersion(t, h, "", "0.9.0")
	check(legacy, strings.Repeat("界", 512)+"…", false)
	old := queue("mcp-namespace-one-service")
	first, _ := selectMCPVersion(t, h, oldGraph, "1.0.0")
	check(old, strings.Repeat("界", 512)+"…", false)
	current := queue("mcp-namespace-original")
	second, pkg := selectMCPVersion(t, h, first, "1.2.0")
	check(current, strings.Repeat("界", 512)+"…", true)
	changed := queue("mcp-namespace-new")
	check(changed, strings.Repeat("界", 32)+"…", true)
	retained := queue("mcp-namespace-retained")
	activateDelegation(t, h, "mcp-namespace-rollback", second, first, "mcp-owner-1.0.0")
	if err := h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(retained, strings.Repeat("界", 32)+"…", true)
	rolled := queue("mcp-namespace-rolled")
	check(rolled, strings.Repeat("界", 512)+"…", true)
	if _, err := h.Store.Deactivate(ctx, "mcp-namespace-disable", first); err != nil {
		t.Fatal(err)
	}
	disabled := queue("mcp-namespace-disabled")
	check(disabled, "", true)
}

func TestMCPPluginNamespaceProjectionLeavesWriterAndRechecksForget(t *testing.T) {
	for _, mode := range []string{"writer", "forget", "forget-after", "forget-retry", "deactivate", "quarantine", "quarantine-after", "quarantine-retry", "source-change", "source-change-after", "remove-config"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			defs, _ := mcpPluginDefinitions(t, "  Original namespace\nnot part of the summary")
			s, r, _, _ := childFixture(t, append(defs, codemode.Definition(), tool.SearchDefinition())...)
			catalog := rsi.NewCatalog(filepath.Dir(s.path))
			catalog.Launcher = runtimeFeatureHost(t)
			r.ConfigurePlugins(catalog)
			h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
			graph, _ := selectMCPVersion(t, h, "", "1.0.0")
			reached := 0
			owner := idAgent(t, s)
			baseMode := strings.TrimSuffix(strings.TrimSuffix(mode, "-after"), "-retry")
			var fired1 atomic.Bool
			s.MainBoundary = func(point string) {
				wanted := "before_mcp_namespace_projection"
				if strings.HasSuffix(mode, "-after") {
					wanted = "after_mcp_namespace_projection"
				}
				if strings.HasSuffix(mode, "-retry") {
					wanted = "before_mcp_namespace_retry"
				}
				if point != wanted {
					return
				}
				if !fired1.CompareAndSwap(false, true) {
					return
				}
				reached++
				writeCtx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				if err := s.Transaction(writeCtx, func(tx *sql.Tx) error {
					if baseMode == "forget" {
						return r.revokeMemory(writeCtx, tx, owner)
					}
					_, err := tx.ExecContext(writeCtx, `UPDATE agent_config SET config_version=config_version+1 WHERE singleton=1`)
					return err
				}); err != nil {
					t.Fatal("namespace candidate was evaluated under a SQL writer", err)
				}
				if baseMode == "deactivate" {
					if _, err := h.Store.Deactivate(ctx, "mcp-projection-disable", graph); err != nil {
						t.Fatal(err)
					}
				}
				if baseMode == "quarantine" {
					if err := h.Store.Quarantine(ctx, graph); err != nil {
						t.Fatal(err)
					}
				}
				if baseMode == "source-change" || baseMode == "remove-config" {
					replacement := []tool.Definition{}
					if baseMode == "source-change" {
						for _, spec := range r.registry.Describe() {
							if strings.HasPrefix(spec.Name, "mcp__managed__") {
								d, _ := r.registry.Resolve(spec.Name)
								changed := "New owner namespace source"
								d.NamespaceSource = &changed
								replacement = append(replacement, d)
							}
						}
					}
					if err := r.registry.ReplaceGroup("mcp__managed__", replacement); err != nil {
						t.Fatal(err)
					}
				}
			}
			requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			parts := "original private ingress must not be resubmitted by projection"
			receipt, err := r.Submit(requestCtx, "mcp-namespace-"+mode, conversation.Text(parts))
			if reached != 1 {
				t.Fatal("did not reach actual uncached projection", reached, err)
			}
			if baseMode == "forget" || baseMode == "quarantine" || baseMode == "source-change" || baseMode == "remove-config" {
				if err == nil || baseMode != "quarantine" && !errors.Is(err, agent.ErrClaimLost) {
					t.Fatal("revoked projection created a new Run", receipt, err)
				}
				if scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT count(*) FROM conversation_intent WHERE instr(message_json,?)>0`, parts) != 0 {
					t.Fatal("rolled-back private ingress was resurrected after revocation")
				}
				return
			}
			if err != nil || receipt.RunID == "" || scalar(t, s, `SELECT count(*) FROM run`) != 1 || scalar(t, s, `SELECT count(*) FROM conversation_intent WHERE instr(message_json,?)>0`, parts) != 1 {
				t.Fatal("projection retry lost or duplicated original ingress", receipt, err)
			}
			var raw string
			if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, receipt.RunID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var specs []tool.Spec
			_ = json.Unmarshal([]byte(raw), &specs)
			want := "Original namespace"
			if mode == "deactivate" {
				want = ""
			}
			count := 0
			for _, spec := range specs {
				if strings.HasPrefix(spec.Name, "mcp__managed__") {
					count++
					if spec.NamespaceDescription != want {
						t.Fatal("retry froze a stale graph's namespace", spec.NamespaceDescription, want)
					}
				}
			}
			if count != 2 {
				t.Fatal("did not verify the actual frozen MCP namespace", count)
			}
		})
	}
}

func TestMCPPluginNamespaceOriginalLargeInstructionsUseBoundedDefaultProjection(t *testing.T) {
	bin := runtimeFeatureHost(t)
	root := t.TempDir()
	instructions := strings.Repeat("原", 340000) + "\nUNSELECTED_SERVER_INSTRUCTION_TAIL"
	initialize, _ := json.Marshal(map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "instructions": instructions})
	if len(initialize) > (1<<20)-256 || len(initialize) < 1<<19 {
		t.Fatal("fixture does not cover the original large initialize frame", len(initialize))
	}
	if err := os.WriteFile(filepath.Join(root, "mcp-initialize.json"), initialize, 0600); err != nil {
		t.Fatal(err)
	}
	defs, err := mcp.Definitions(context.Background(), mcp.Config{Name: "managed", Command: bin, Directory: root, Args: []string{"-test-mcp-server", root}, Tools: map[string]string{"read": "read_only", "send": "external_effect"}})
	if err != nil {
		t.Fatal(err)
	}
	s, r, a, p := childFixture(t, append(defs, codemode.Definition(), tool.SearchDefinition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.0")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "bounded namespace observed"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `return (await describeNamespace("managed")).description;`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	projections := 0
	s.MainBoundary = func(point string) {
		if point == "before_mcp_namespace_projection" {
			projections++
		}
	}
	mustSubmit(t, a, "mcp-large-namespace", "Describe the original namespace")
	mustDrain(t, a)
	if projections != 1 {
		t.Fatal("pure namespace was not shared across same-source snapshots", projections)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var output []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &output) != nil || len(output) != 1 || output[0].Value != strings.Repeat("原", 512)+"…" {
		t.Fatal("large original instructions were truncated before the actual policy", raw)
	}
	p.mu.Lock()
	requests, _ := json.Marshal(p.requests)
	p.mu.Unlock()
	if strings.Contains(string(requests), "UNSELECTED_SERVER_INSTRUCTION_TAIL") || scalar(t, s, `SELECT count(*) FROM capability_snapshot WHERE instr(tools_json,'UNSELECTED_SERVER_INSTRUCTION_TAIL')>0`) != 0 {
		t.Fatal("raw namespace source leaked into model requests or snapshots")
	}
}

func TestMCPPluginAcceptedNamespaceCannotSplitOriginalUnicode(t *testing.T) {
	defs, _ := mcpPluginDefinitions(t, "甲乙原文")
	s, r, _, _ := childFixture(t, append(defs, codemode.Definition(), tool.SearchDefinition())...)
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.3.0") // independent owner policy accepts its ASCII case
	if receipt, err := r.Submit(context.Background(), "mcp-bad-namespace", conversation.Text("Do not replace namespace source")); err == nil || receipt.RunID != "" && scalar(t, s, `SELECT count(*) FROM run WHERE run_id=?`, receipt.RunID) != 0 {
		t.Fatal("accepted bad candidate bypassed original Unicode source proof", receipt, err)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 0 {
		t.Fatal("bad namespace restored the original native policy")
	}
}
