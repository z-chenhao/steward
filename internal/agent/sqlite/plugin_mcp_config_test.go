package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestMCPPluginCurrentConfigurationFencesDispatchReleaseAndCommit(t *testing.T) {
	for _, mode := range []string{"before-call", "replace-peer", "after-call", "before-commit", "stop-commit", "quarantine-commit", "description-only"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			defs, root := mcpPluginDefinitions(t)
			if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(`{"content":[{"type":"text","text":"ORIGINAL_CONFIG_SCOPE_RESULT"}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
			r.ConfigureJavaScript(realCodeRuntime(t))
			catalog := rsi.NewCatalog(filepath.Dir(s.path))
			catalog.Launcher = runtimeFeatureHost(t)
			r.ConfigurePlugins(catalog)
			h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
			graph, _ := selectMCPVersion(t, h, "", "1.0.0")
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "current resource status observed"}, nil
				}
				raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__send({text:"exact configured peer action"});`})
				return calls(codemode.ToolName, string(raw)), nil
			})
			mustSubmit(t, a, "mcp-config-"+mode, "Perform the exact requested action")
			mustDrain(t, a)
			var approval, hash string
			if err := s.db.QueryRow(`SELECT approval_id,frozen_arguments_hash FROM approval WHERE status='pending'`).Scan(&approval, &hash); err != nil {
				t.Fatal(err)
			}
			sources, reached := 0, 0
			replacementRoot := ""
			var fired1 atomic.Bool
			s.MainBoundary = func(point string) {
				if point == "after_mcp_source" {
					sources++
				}
				commitBoundary := mode == "before-commit" || mode == "stop-commit" || mode == "quarantine-commit"
				wanted := point == "after_mcp_source" && ((mode == "before-call" || mode == "replace-peer" || mode == "description-only") && sources == 1 || mode == "after-call" && sources == 2) || commitBoundary && point == "before_mcp_method_commit"
				if !wanted {
					return
				}
				if !fired1.CompareAndSwap(false, true) {
					return
				}
				reached++
				if mode == "stop-commit" {
					if err := h.Store.Stop(ctx, true); err != nil {
						t.Fatal(err)
					}
					return
				}
				if mode == "quarantine-commit" {
					if err := h.Store.Quarantine(ctx, graph); err != nil {
						t.Fatal(err)
					}
					return
				}
				replacement := []tool.Definition{}
				if mode == "description-only" {
					for _, d := range defs {
						text := "Changed namespace presentation only"
						d.NamespaceSource, d.Spec.NamespaceDescription = &text, text
						replacement = append(replacement, d)
					}
				}
				if mode == "replace-peer" {
					replacement, replacementRoot = mcpPluginDefinitions(t)
				}
				if err := r.registry.ReplaceGroup("mcp__managed__", replacement); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Decide(ctx, idAgent(t, s), approval, hash, "approved"); err != nil {
				t.Fatal(err)
			}
			_ = a.Drain(ctx) // an already accepted Run may fail on explicit revocation
			if reached != 1 {
				t.Fatal("did not reach the real accepted configuration boundary", mode, sources)
			}
			data, err := os.ReadFile(filepath.Join(root, "mcp-effects.jsonl"))
			if mode == "before-call" || mode == "replace-peer" {
				if !os.IsNotExist(err) {
					t.Fatal("revoked peer was dispatched", string(data), err)
				}
			} else if err != nil || strings.Count(string(data), "exact configured peer action") != 1 {
				t.Fatal("accepted original effect count changed", string(data), err)
			}
			if replacementRoot != "" {
				if _, err := os.Stat(filepath.Join(replacementRoot, "mcp-effects.jsonl")); !os.IsNotExist(err) {
					t.Fatal("old approved action was redirected to the replacement peer", err)
				}
			}
			if mode == "description-only" {
				if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='succeeded' AND json_extract(result_json,'$.text')='ORIGINAL_CONFIG_SCOPE_RESULT'`) != 1 {
					t.Fatal("presentation update rebound or revoked original execution authority")
				}
				return
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'ORIGINAL_CONFIG_SCOPE_RESULT')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE instr(request_json,'ORIGINAL_CONFIG_SCOPE_RESULT')>0`) != 0 {
				t.Fatal("revoked source was released to the model or public receipt")
			}
			if mode == "after-call" || mode == "before-commit" || mode == "stop-commit" || mode == "quarantine-commit" {
				if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='unknown' AND replay_policy='never' AND attempt_count=1`) != 1 {
					t.Fatal("late revocation failed to record original effect uncertainty")
				}
			}
			if err := r.Recover(ctx); err != nil && !errors.Is(err, llm.ModelIncompatible) && !errors.Is(err, rsi.ErrRevoked) {
				t.Fatal(err)
			}
			_ = a.Drain(ctx)
			later, readErr := os.ReadFile(filepath.Join(root, "mcp-effects.jsonl"))
			if string(later) != string(data) || ((mode == "before-call" || mode == "replace-peer") && !os.IsNotExist(readErr)) {
				t.Fatal("recovery repeated an effect after configuration revocation", string(later), readErr)
			}
		})
	}
}
