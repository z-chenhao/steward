package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestMCPPluginActualMalformedReplyAndAcceptedBadMethodKeepOriginalEffectTruth(t *testing.T) {
	for _, mode := range []string{"null-content", "nontext", "null-error", "known-error", "known-error-method-failure", "bad-method"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			defs, root := mcpPluginDefinitions(t)
			reply := `{"content":[{"type":"text","text":"PRIVATE_MCP_DIAGNOSTIC"}]}`
			switch mode {
			case "null-content":
				reply = `{"content":null}`
			case "nontext":
				reply = `{"content":[{"type":"image","data":"PRIVATE_MCP_DIAGNOSTIC"}]}`
			case "null-error":
				reply = `{"content":[{"type":"text","text":"PRIVATE_MCP_DIAGNOSTIC"}],"isError":null}`
			case "known-error", "known-error-method-failure":
				reply = `{"content":[{"type":"text","text":"PRIVATE_MCP_DIAGNOSTIC"}],"isError":true}`
			}
			if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(reply), 0600); err != nil {
				t.Fatal(err)
			}
			s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
			r.ConfigureJavaScript(realCodeRuntime(t))
			catalog := rsi.NewCatalog(filepath.Dir(s.path))
			catalog.Launcher = runtimeFeatureHost(t)
			r.ConfigurePlugins(catalog)
			h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
			version := "1.0.0"
			if mode == "bad-method" {
				version = "1.5.0" // accepted independent policy; original host proof must win
			}
			if mode == "known-error-method-failure" {
				version = "1.6.0"
			}
			selectMCPVersion(t, h, "", version)
			p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				if req.Messages[len(req.Messages)-1].Role == "tool" {
					return llm.GenerationResult{Text: "original effect status reported"}, nil
				}
				raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__send({text:"exact accepted original effect"});`})
				return calls(codemode.ToolName, string(raw)), nil
			})
			sources := 0
			s.MainBoundary = func(point string) {
				if point == "after_mcp_source" {
					sources++
				}
			}
			mustSubmit(t, a, "mcp-negative-"+mode, "Perform the exact accepted action")
			mustDrain(t, a)
			if sources != 0 {
				t.Fatal("managed peer accessed before exact approval", sources)
			}
			var approval, hash string
			if err := s.db.QueryRow(`SELECT approval_id,frozen_arguments_hash FROM approval WHERE status='pending'`).Scan(&approval, &hash); err != nil {
				t.Fatal(err)
			}
			if err := r.Decide(ctx, idAgent(t, s), approval, hash, "approved"); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if err := r.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			data, err := os.ReadFile(filepath.Join(root, "mcp-effects.jsonl"))
			if err != nil || strings.Count(string(data), "exact accepted original effect") != 1 || sources != 2 {
				t.Fatal("original dispatch count changed or native path used", string(data), sources, err)
			}
			if mode == "known-error" || mode == "known-error-method-failure" {
				if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='failed' AND replay_policy='never' AND attempt_count=1 AND json_extract(result_json,'$.error.code')='INVALID_ARGUMENT'`) != 1 {
					t.Fatal("actual peer error truth was replaced by plugin output")
				}
			} else if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='unknown' AND replay_policy='never' AND attempt_count=1`) != 1 {
				t.Fatal("malformed reply or bad method invented certainty")
			}
			p.mu.Lock()
			requests, _ := json.Marshal(p.requests)
			p.mu.Unlock()
			if strings.Contains(string(requests), "PRIVATE_MCP_DIAGNOSTIC") || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE instr(result_json,'PRIVATE_MCP_DIAGNOSTIC')>0`) != 0 {
				t.Fatal("unaccepted source or error body reached a model or public receipt")
			}
		})
	}
}
