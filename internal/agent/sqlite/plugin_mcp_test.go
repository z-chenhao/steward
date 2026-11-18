package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/internal/tool/mcp"
	mcpfake "github.com/z-chenhao/assistant/internal/tool/mcp/fake"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func mcpPluginDefinitions(t *testing.T, description ...string) ([]tool.Definition, string) {
	t.Helper()
	bin := runtimeFeatureHost(t)
	root := t.TempDir()
	text := ""
	if len(description) == 1 {
		text = description[0]
	}
	defs, err := mcp.Definitions(context.Background(), mcp.Config{Name: "managed", Command: bin, Directory: root, Args: []string{"-test-mcp-server", root}, Tools: map[string]string{"read": "read_only", "send": "external_effect"}, Description: text})
	if err != nil {
		t.Fatal(err)
	}
	return defs, root
}

func mcpVersionPackage(t *testing.T, version string) string {
	t.Helper()
	directory := t.TempDir()
	path, err := filepath.Abs("../../plugin/mcpbridge/method.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" || version == "1.4.0" {
		code = strings.Replace(code, `Separator: ""`, `Separator: "\n"`, 1)
	}
	if version == "1.6.0" {
		code = strings.Replace(code, "plan := contract.Plan", `if reply.IsError {return rsiplugin.Result{},errors.New("candidate failed after actual peer error")};plan := contract.Plan`, 1)
	}
	if version == "1.5.0" {
		code = strings.Replace(code, "Index: index,", "Index: index+100,", 1)
	}
	if version == "0.9.0" {
		code = strings.Replace(code, ", contract.Namespace: namespace", "", 1)
	}
	override := filepath.Join(t.TempDir(), "method.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{path: override}
	if version == "1.2.0" || version == "1.3.0" || version == "1.4.0" {
		namespacePath, err := filepath.Abs("../../plugin/mcpbridge/namespace.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(namespacePath)
		if err != nil {
			t.Fatal(err)
		}
		code := string(raw)
		if version == "1.2.0" || version == "1.4.0" {
			code = strings.ReplaceAll(code, "512", "32")
		}
		if version == "1.3.0" {
			code = strings.Replace(code, "Start: start,", "Start: start+1,", 1)
		}
		file := filepath.Join(t.TempDir(), "namespace.go")
		if err := os.WriteFile(file, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[namespacePath] = file
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../../cmd/plugins/mcp-bridge").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/mcp-bridge/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	if version == "0.9.0" {
		manifest.Provides = manifest.Provides[:1]
		manifest.Requests = []string{"resource.call"}
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func selectMCPVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, mcpVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "mcp-bridge", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "mcp-bridge", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	separator := ""
	if version == "1.1.0" || version == "1.4.0" {
		separator = "\n"
	}
	expected, _ := json.Marshal(contract.Plan{Parts: []contract.Part{{Index: 0, End: 3}, {Index: 1, End: 3}}, Separator: separator})
	if version == "1.5.0" {
		expected, _ = json.Marshal(contract.Plan{Parts: []contract.Part{{Index: 100, End: 3}, {Index: 101, End: 3}}, Separator: separator})
	}
	policy := rsi.Policy{ID: "mcp-owner-" + version, AllowedRequests: []string{"resource.call"}, AllowedContracts: []string{contract.Method}, Cases: []rsi.Case{{InstanceID: "mcp-bridge", Contract: contract.Method, Input: json.RawMessage(`{}`), Expected: expected, Resources: []rsi.ResourceFixture{
		{Contract: contract.Source, Key: "list:0", Input: json.RawMessage(`{"phase":"list","cursor":""}`), Output: json.RawMessage(`{"next_cursor":"page-two","text":[],"is_error":false}`)},
		{Contract: contract.Source, Key: "list:1", Input: json.RawMessage(`{"phase":"list","cursor":"page-two"}`), Output: json.RawMessage(`{"next_cursor":"","text":[],"is_error":false}`)},
		{Contract: contract.Source, Key: "call", Input: json.RawMessage(`{"phase":"call","cursor":""}`), Output: json.RawMessage(`{"next_cursor":"","text":["甲","乙"],"is_error":false}`)},
	}}}}
	if version != "0.9.0" {
		policy.AllowedRequests = append(policy.AllowedRequests, "input.read")
		policy.AllowedContracts = append(policy.AllowedContracts, contract.Namespace)
		expected := json.RawMessage(`{"start":2,"end":20,"truncated":false}`)
		if version == "1.3.0" {
			expected = json.RawMessage(`{"start":3,"end":20,"truncated":false}`)
		}
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "mcp-bridge", Contract: contract.Namespace, Input: json.RawMessage(`{"input_id":"source"}`), Inputs: []rsi.InputData{{ID: "source", Data: []byte("  Original namespace\nSecond line")}}, Expected: expected})
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "mcp-"+version, baseline, g.Revision, policy.ID)
	return g.Revision, pkg.Digest
}

func TestMCPPluginActualCodeModeUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	defs, root := mcpPluginDefinitions(t)
	if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(`{"content":[{"type":"text","text":"甲"},{"type":"text","text":"乙"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		args, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__read({text:"accepted-original"});`})
		return calls(codemode.ToolName, string(args)), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Read the original MCP peer")
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
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='mcp__managed__read'`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got tool.Result
		if json.Unmarshal([]byte(raw), &got) != nil || got.Error != nil || got.Text != want {
			t.Fatal("wrong real MCP result", raw, want)
		}
		selected, err := r.managedRunService(ctx, id, contract.Method)
		if err != nil || selected != managed {
			t.Fatal("Run migration boundary changed", selected, managed, err)
		}
		if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='mcp__managed__read' AND parent_invocation_id IS NOT NULL AND status='succeeded'`, id) != 1 {
			t.Fatal("result bypassed ordinary nested Tool admission")
		}
	}
	legacy := queue("mcp-legacy")
	h := rsi.Host{Catalog: *r.host.pluginCatalog.Load(), Store: s.Plugins()}
	first, _ := selectMCPVersion(t, h, "", "1.0.1")
	check(legacy, "甲乙", false)
	old := queue("mcp-old")
	second, pkg := selectMCPVersion(t, h, first, "1.1.0")
	check(old, "甲乙", true)
	current := queue("mcp-current")
	check(current, "甲\n乙", true)
	retained := queue("mcp-retained")
	activateDelegation(t, h, "mcp-rollback", second, first, "mcp-owner-1.0.1")
	if err := h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	check(retained, "甲\n乙", true)
	rolled := queue("mcp-rolled")
	check(rolled, "甲乙", true)
	if _, err := h.Store.Deactivate(ctx, "mcp-disable", first); err != nil {
		t.Fatal(err)
	}
	disabled := queue("mcp-disabled")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='mcp__managed__read' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 {
		t.Fatal("deactivated method silently restored native MCP")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("MCP method created a second effect ledger")
	}
}

func TestMCPPluginActualApprovalAndUnknownNeverRepeats(t *testing.T) {
	ctx := context.Background()
	defs, root := mcpPluginDefinitions(t)
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: *r.host.pluginCatalog.Load(), Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.1")
	boundaries := 0
	s.MainBoundary = func(point string) {
		if point == "after_mcp_source" {
			boundaries++
		}
	}
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "uncertainty reported"}, nil
		}
		args, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__send({text:"exact approved message",behavior:"unknown"});`})
		return calls(codemode.ToolName, string(args)), nil
	})
	mustSubmit(t, a, "mcp-effect", "Send the requested message")
	mustDrain(t, a)
	if _, err := os.Stat(filepath.Join(root, "mcp-effects.jsonl")); !os.IsNotExist(err) {
		t.Fatal("effect ran before exact approval", err)
	}
	var id, hash string
	if err := s.db.QueryRow(`SELECT approval_id,frozen_arguments_hash FROM approval WHERE status='pending'`).Scan(&id, &hash); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide(ctx, idAgent(t, s), id, hash, "approved"); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	for attempt := 0; attempt < 2; attempt++ {
		if err := r.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		mustDrain(t, a)
	}
	raw, err := os.ReadFile(filepath.Join(root, "mcp-effects.jsonl"))
	if err != nil || strings.Count(string(raw), "exact approved message") != 1 {
		t.Fatal("unknown effect replayed", string(raw), err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='unknown' AND replay_policy='never' AND attempt_count=1`) != 1 {
		t.Fatalf("original never/unknown receipt not retained: %s", raw)
	}
	if boundaries != 2 {
		t.Fatal("effect did not use the managed list/call ports exactly once", boundaries)
	}
}

func TestMCPPluginLateForgetAndGuardianCannotPublishPrivateResult(t *testing.T) {
	for _, point := range []string{"after_mcp_source", "after_mcp_method", "before_mcp_method_commit"} {
		for _, mode := range []string{"forget", "quarantine"} {
			t.Run(point+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				defs, root := mcpPluginDefinitions(t)
				if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(`{"content":[{"type":"text","text":"PRIVATE_MCP_RESULT"}]}`), 0600); err != nil {
					t.Fatal(err)
				}
				s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
				r.ConfigureJavaScript(realCodeRuntime(t))
				catalog := rsi.NewCatalog(filepath.Dir(s.path))
				catalog.Launcher = runtimeFeatureHost(t)
				r.ConfigurePlugins(catalog)
				h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
				revision, _ := selectMCPVersion(t, h, "", "1.0.1")
				p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
					if req.Messages[len(req.Messages)-1].Role == "tool" {
						return llm.GenerationResult{Text: "observed"}, nil
					}
					raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__read({text:"read authorized source"});`})
					return calls(codemode.ToolName, string(raw)), nil
				})
				reached, sources := 0, 0
				owner := idAgent(t, s)
				var fired2 atomic.Bool
				s.MainBoundary = func(current string) {
					if current == "after_mcp_source" {
						sources++
					}
					if current != point || point == "after_mcp_source" && sources < 2 {
						return
					}
					if !fired2.CompareAndSwap(false, true) {
						return
					}
					reached++
					if mode == "forget" {
						if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
							t.Fatal(err)
						}
					} else if err := h.Store.Quarantine(ctx, revision); err != nil {
						t.Fatal(err)
					}
				}
				mustSubmit(t, a, "mcp-private", "Read the authorized source")
				_ = a.Drain(ctx) // revocation may interrupt its already-running Loop
				if reached != 1 || sources != 2 {
					t.Fatal("did not reach the actual managed result boundary", reached, sources)
				}
				if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_MCP_RESULT')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM model_generation WHERE instr(request_json,'PRIVATE_MCP_RESULT')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
					t.Fatal("revoked managed result was published or returned to the model")
				}
			})
		}
	}
}

func TestMCPPluginMissingCarrierKeepsFrozenManagedBoundary(t *testing.T) {
	defs, _ := mcpPluginDefinitions(t)
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.1")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "unavailable reported"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__read({text:"must not use native"});`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	receipt := mustSubmit(t, a, "mcp-missing-carrier", "Read the selected peer")
	if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
		t.Fatal(err)
	}
	r.plugins = nil
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__read' AND status='failed' AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
		t.Fatal("missing carrier opted a managed Run back into native")
	}
	var managed int
	if err := s.db.QueryRow(`SELECT count(*) FROM rsi_run r JOIN run u USING(run_id),json_each(r.managed_services_json) s WHERE u.kind=? AND s.value=?`, string(agent.WorkerRole), contract.Method).Scan(&managed); err != nil || managed != 1 {
		t.Fatal("frozen managed boundary disappeared", managed, err)
	}
}

func TestMCPPluginRealReleaseRecoveryKeepsOriginalNeverCallAndGraph(t *testing.T) {
	ctx := context.Background()
	defs, root := mcpPluginDefinitions(t)
	args, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__send({text:"effect before lost receipt"});`})
	calls := []llm.ToolCall{{ID: "original-code", Name: codemode.ToolName, Arguments: string(args)}}
	var h rsi.Host
	var original string
	s, r, owner, executor, exchange := admitPolicyRun(t, append(defs, codemode.Definition()), calls, false, func(s *Store, r *Runs) {
		r.ConfigureJavaScript(realCodeRuntime(t))
		catalog := rsi.NewCatalog(filepath.Dir(s.path))
		catalog.Launcher = runtimeFeatureHost(t)
		r.ConfigurePlugins(catalog)
		h = rsi.Host{Catalog: catalog, Store: s.Plugins()}
		original, _ = selectMCPVersion(t, h, "", "1.0.1")
	})
	stop := keepRunClaim(r, owner)
	defer stop()
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("exact approval was not requested", err)
	}
	var approval, hash string
	if err := s.db.QueryRow(`SELECT approval_id,frozen_arguments_hash FROM approval WHERE status='pending'`).Scan(&approval, &hash); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide(ctx, idAgent(t, s), approval, hash, "approved"); err != nil {
		t.Fatal(err)
	}
	reached, sources := 0, 0
	var fired1 atomic.Bool
	s.MainBoundary = func(point string) {
		if point == "after_mcp_source" {
			sources++
		}
		if point != "after_mcp_method" {
			return
		}
		if !fired1.CompareAndSwap(false, true) {
			return
		}
		reached++
		stop()
		if err := r.Release(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, agent.ErrClaimLost) || reached != 1 || sources != 2 {
		t.Fatal("fixture did not interrupt the actual effect before receipt acceptance", reached, sources, err)
	}
	selectMCPVersion(t, h, original, "1.1.0")
	if next, err := r.Next(ctx); err != nil || next != owner.RunID() {
		t.Fatal("real recovery did not find the original Run", next, err)
	}
	next, err := r.Claim(ctx, owner.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	bound, err := h.Store.Binding(ctx, next.RunID())
	if err != nil || bound != original {
		t.Fatal("recovery substituted the active method", bound, original, err)
	}
	executor.Store = r.ToolStore(next)
	// This is the first dependent operation after actual recovery; no select,
	// rebind or fixture repair is allowed between recovery and this call.
	if _, err := executor.Execute(ctx, exchange, calls); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "mcp-effects.jsonl"))
	if err != nil || strings.Count(string(raw), "effect before lost receipt") != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__send' AND status='unknown' AND replay_policy='never' AND attempt_count=1`) != 1 {
		t.Fatal("recovery repeated the accepted external effect", string(raw), err)
	}
}

func mcpPluginPagedDefinitions(t *testing.T, pageCount, toolsPerPage, allowedCount int) ([]tool.Definition, string) {
	t.Helper()
	bin := runtimeFeatureHost(t)
	root := t.TempDir()
	allowlist := map[string]string{}
	pages := map[string]any{}
	for page := 0; page < pageCount; page++ {
		items := []any{}
		for offset := 0; offset < toolsPerPage; offset++ {
			name := fmt.Sprintf("tool_%03d", page*toolsPerPage+offset)
			if page == pageCount-1 && offset == toolsPerPage-1 {
				name = "read"
			}
			if page*toolsPerPage+offset < allowedCount-1 || name == "read" {
				allowlist[name] = "read_only"
			}
			items = append(items, map[string]any{"name": name, "description": "Original allowed fixture tool", "inputSchema": json.RawMessage(mcpfake.Schema)})
		}
		cursor, next := "", ""
		if page > 0 {
			cursor = fmt.Sprintf("page-%d", page)
		}
		if page < pageCount-1 {
			next = fmt.Sprintf("page-%d", page+1)
		}
		pages[cursor] = map[string]any{"tools": items, "nextCursor": next}
	}
	raw, _ := json.Marshal(pages)
	if err := os.WriteFile(filepath.Join(root, "mcp-pages.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	defs, err := mcp.Definitions(context.Background(), mcp.Config{Name: "managed", Command: bin, Directory: root, Args: []string{"-test-mcp-server", root}, Tools: allowlist})
	if err != nil || len(defs) != allowedCount {
		t.Fatal("startup did not preserve the complete actual peer catalog", len(defs), err)
	}
	return defs, root
}

func TestMCPPluginActualPagedFiveHundredToolsUseCompleteFrozenCatalog(t *testing.T) {
	defs, _ := mcpPluginPagedDefinitions(t, 10, 50, 500)
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.1")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "last catalog page observed"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `const d=await describeTool("mcp__managed__read"); if(d.name!=="mcp__managed__read"||JSON.parse(d.inputSchema).type!=="object")throw Error("wrong frozen description"); return await tools.mcp__managed__read({text:"actual last-page source"});`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	sources := 0
	s.MainBoundary = func(point string) {
		if point == "after_mcp_source" {
			sources++
		}
	}
	mustSubmit(t, a, "mcp-five-hundred", "Read the last catalog page's source")
	mustDrain(t, a)
	if sources != 11 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__read' AND status='succeeded' AND json_extract(result_json,'$.text')='actual last-page source'`) != 1 {
		t.Fatal("managed rediscovery skipped pages or lost the actual last-page tool", sources)
	}
	if scalar(t, s, `SELECT count(*) FROM code_event WHERE json_extract(message_json,'$.toolName')='describeTool'`) == 0 {
		t.Fatal("actual CodeMode progressive description did not run")
	}
}

func TestMCPPluginOriginalFrameWithFortyThousandTextBlocks(t *testing.T) {
	defs, root := mcpPluginDefinitions(t)
	// Empty valid blocks are still part of the original supported response.
	// Their position plan exceeds one ordinary frame and must use SDK chunks.
	body := `{"content":[` + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":""},`, 40000), ",") + `]}`
	if len(body) >= 1<<20 || len(body) < (1<<20)-16384 {
		t.Fatal("fixture is not close to the original peer frame limit", len(body))
	}
	if err := os.WriteFile(filepath.Join(root, "mcp-result.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.1")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "complete original reply accepted"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__read({text:"original frame"});`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	sources := 0
	s.MainBoundary = func(point string) {
		if point == "after_mcp_source" {
			sources++
		}
	}
	mustSubmit(t, a, "mcp-original-frame", "Read the full original peer response")
	mustDrain(t, a)
	if sources != 2 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__read' AND status='succeeded' AND coalesce(json_extract(result_json,'$.text'),'')=''`) != 1 {
		t.Fatal("managed method narrowed the original peer frame support", sources)
	}
}

// Exercise the original peer catalog ceiling, including entries outside the
// owner allowlist. The existing CodeMode transport permits 1024 tools, so keep
// its selected set at the separately verified 500-tool size. The actual method
// must still rediscover all 4096 peer entries over all 16 pages.
func TestMCPPluginOriginalSixteenPageFourThousandNinetySixEntryCatalog(t *testing.T) {
	defs, _ := mcpPluginPagedDefinitions(t, 16, 256, 500)
	s, r, a, p := childFixture(t, append(defs, codemode.Definition())...)
	r.ConfigureJavaScript(realCodeRuntime(t))
	catalog := rsi.NewCatalog(filepath.Dir(s.path))
	catalog.Launcher = runtimeFeatureHost(t)
	r.ConfigurePlugins(catalog)
	h := rsi.Host{Catalog: catalog, Store: s.Plugins()}
	selectMCPVersion(t, h, "", "1.0.1")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "original maximum catalog accepted"}, nil
		}
		raw, _ := json.Marshal(map[string]string{"source": `return await tools.mcp__managed__read({text:"original sixteen-page boundary"});`})
		return calls(codemode.ToolName, string(raw)), nil
	})
	sources := 0
	s.MainBoundary = func(point string) {
		if point == "after_mcp_source" {
			sources++
		}
	}
	mustSubmit(t, a, "mcp-original-catalog-boundary", "Read the original last-page tool")
	if err := a.Drain(context.Background()); err != nil {
		var operation *agent.OperationError
		if errors.As(err, &operation) {
			t.Fatalf("%v: cause=%v", err, operation.Cause)
		}
		t.Fatal(err)
	}
	if sources != 17 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__managed__read' AND status='succeeded' AND json_extract(result_json,'$.text')='original sixteen-page boundary'`) != 1 {
		t.Fatal("managed method narrowed the original catalog boundary", sources)
	}
	var toolsJSON string
	if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id JOIN tool_invocation i USING(run_id) WHERE i.tool_name='mcp__managed__read'`).Scan(&toolsJSON); err != nil {
		t.Fatal(err)
	}
	var frozen []tool.Spec
	if err := json.Unmarshal([]byte(toolsJSON), &frozen); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, spec := range frozen {
		if strings.HasPrefix(spec.Name, "mcp__managed__") {
			count++
		}
	}
	if count != 500 {
		t.Fatal("accepted snapshot changed the original owner allowlist", count)
	}
}
