package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	directorycontract "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The required system gate supplies the actual built distribution. Ordinary
// package tests do not manufacture a partial bundle and call it default proof.
func TestConnectedAppsDefaultProductionCompositionAndColdLifecycle(t *testing.T) {
	for _, mode := range []string{"managed33", "managed32", "prior30", "prior29", "prior28", "prior24", "prior23", "prior22"} {
		t.Run(mode, func(t *testing.T) {
			testConnectedAppsDefaultLifecycle(t, mode)
		})
	}
}

func testConnectedAppsDefaultLifecycle(t *testing.T, mode string) {
	managed := mode != "prior22"
	modern := mode == "managed33" || mode == "managed32" || mode == "prior30"
	discovery := modern || mode == "prior29" || mode == "prior28" || mode == "prior24"
	t.Helper()
	carrier := os.Getenv("STEWARD_TEST_APP_DEFAULT_BUNDLE")
	if carrier == "" {
		t.Skip("requires actual distribution; exercised by connected-apps-plugins system gate")
	}
	carrier, err := filepath.Abs(carrier)
	if err != nil {
		t.Fatal(err)
	}
	directories, err := shippedPluginDirectories(carrier)
	if err != nil || directories.Apps == "" {
		t.Fatal("actual default distribution missing Apps", err)
	}
	// These named historical fixtures predate the seven later feature packages.
	directories.ModelConnections, directories.Feishu, directories.CapabilityForms, directories.Terminal = "", "", "", ""
	directories.ReferencePricing, directories.WebWorkbench, directories.ServiceWatchdog = "", "", ""
	ctx := context.Background()
	secrets := memory.New()
	if err := secrets.Put(ctx, connectedapps.DefaultKeyRef, []byte("synthetic-app-project")); err != nil {
		t.Fatal(err)
	}
	var brokerCalls, effects atomic.Int32
	var owner string
	var brokerMu sync.Mutex
	operation := composio.Tool{Slug: "GMAIL_SEND_EMAIL", Name: "Send email", Description: "Original descriptor", Version: "20261101_00", Input: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"}},"required":["to"]}`)}
	operation.Toolkit.Slug = "gmail"
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		brokerCalls.Add(1)
		if request.Header.Get("x-api-key") != "synthetic-app-project" {
			t.Error("missing original SecretStore credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/toolkits":
			item := composio.Toolkit{Slug: "gmail", Name: "Gmail"}
			item.Meta.Description = "Read mail"
			item.Meta.Categories = []composio.Category{{ID: "communication", Name: "Communication"}}
			cursor := "default_next"
			if request.URL.Query().Get("cursor") == cursor {
				cursor = ""
			}
			_ = json.NewEncoder(w).Encode(composio.Catalog{Items: []composio.Toolkit{item}, NextCursor: cursor})
		case "/toolkits/gmail":
			_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail"}`))
		case "/auth_configs":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/tool_router/session":
			var body struct {
				User string `json:"user_id"`
			}
			if json.NewDecoder(request.Body).Decode(&body) != nil || body.User == "" {
				t.Error("missing original owner")
			}
			brokerMu.Lock()
			owner = body.User
			brokerMu.Unlock()
			_, _ = w.Write([]byte(`{"session_id":"trs_default"}`))
		case "/tool_router/session/trs_default/link":
			_, _ = w.Write([]byte(`{"redirect_url":"https://app.composio.dev/link/synthetic","connected_account_id":"ca_personal"}`))
		case "/connected_accounts/ca_personal":
			brokerMu.Lock()
			user := owner
			brokerMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ca_personal", "user_id": user, "status": "ACTIVE", "toolkit": map[string]string{"slug": "gmail"}})
		case "/tools":
			if request.Method != "GET" || request.URL.Query().Get("toolkit_slug") != "gmail" || request.URL.Query().Get("query") != "original query" || request.URL.Query().Get("limit") != "8" {
				t.Error("default method changed original search recipe", request.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []composio.Tool{operation}, "next_cursor": ""})
		case "/tools/GMAIL_SEND_EMAIL":
			if request.URL.Query().Get("version") != operation.Version {
				t.Error("descriptor version changed")
			}
			_ = json.NewEncoder(w).Encode(operation)
		case "/tools/execute/GMAIL_SEND_EMAIL":
			effects.Add(1)
			var body struct {
				Account   string            `json:"connected_account_id"`
				User      string            `json:"user_id"`
				Version   string            `json:"version"`
				Arguments map[string]string `json:"arguments"`
			}
			brokerMu.Lock()
			user := owner
			brokerMu.Unlock()
			if request.Method != "POST" || json.NewDecoder(request.Body).Decode(&body) != nil || body.Account != "ca_personal" || body.User != user || body.Version != operation.Version || body.Arguments["to"] != "friend@example.test" {
				t.Error("default execution changed exact approved authority")
			}
			http.Error(w, "ambiguous after the original POST", http.StatusBadGateway)
		default:
			t.Error("unexpected Apps resource", request.Method, request.URL.Path)
			http.NotFound(w, request)
		}
	}))
	defer broker.Close()
	var modelMu sync.Mutex
	var modelBodies []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			t.Error("invalid model envelope")
			http.Error(w, "invalid", 400)
			return
		}
		raw, _ := json.Marshal(body)
		modelMu.Lock()
		modelBodies = append(modelBodies, string(raw))
		modelMu.Unlock()
		marker := -1
		input := ""
		for index, message := range body.Messages {
			var text string
			_ = json.Unmarshal(message.Content, &text)
			if message.Role == "user" && strings.HasPrefix(text, "APPS_PLUGIN_") {
				marker, input = index, text
			}
		}
		continuation := false
		for index, message := range body.Messages {
			continuation = continuation || marker >= 0 && index > marker && message.Role == "tool"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta any, finish any) {
			value, _ := json.Marshal(map[string]any{"id": "apps-model", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", value)
		}
		if marker >= 0 && !continuation {
			found := false
			for _, declared := range body.Tools {
				found = found || declared.Function.Name == codemode.ToolName
				if strings.HasPrefix(declared.Function.Name, "mcp__apps__") {
					t.Error("production model gained a direct Apps declaration")
				}
			}
			if !found {
				t.Error("real model bridge lost production CodeMode")
			}
			source := `const names=ALL_TOOLS.filter(t=>t.name.startsWith("mcp__apps__b__")).map(t=>t.name);`
			source += `if(names.length){const target=names.find(n=>n.endsWith("__search_tools"));const ranked=await searchTools(target,{limit:1});if(ranked.length!==1||ranked[0].name!==target)throw Error("original authorized app tool not discovered");}`
			if input == "APPS_PLUGIN_NO_ACCOUNT" {
				source += `return names;`
			} else {
				source += `const name=names.find(n=>n.endsWith("__search_tools"));const found=await tools[name]({query:"original query"});`
				if input == "APPS_PLUGIN_EFFECT" {
					source += `const d=found.items[0];return await tools[name.replace(/search_tools$/,"execute")]({tool_slug:d.slug,version:d.version,descriptor_hash:d.descriptor_hash,arguments_json:JSON.stringify({to:"friend@example.test"})});`
				} else {
					source += `return {namespace:(await describeNamespace(name.slice(5,-14))).description,result:found};`
				}
			}
			args, _ := json.Marshal(map[string]string{"source": source})
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "apps-code", "type": "function", "function": map[string]string{"name": codemode.ToolName, "arguments": string(args)}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
		} else {
			chunk(map[string]string{"content": "APPS_RESULT_OBSERVED"}, nil)
			chunk(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sidecarPath, _ := filepath.Abs("../../runtime/javascript/src/sidecar.mjs")
	cfg.LLM = config.LLM{ProviderBridgeCommand: carrier, ProviderBridgeScript: "-model-bridge", ProviderBridgeVersion: "steward-go-v1"}
	cfg.JavaScript = &config.JavaScript{Command: node, Script: sidecarPath}
	var current *App
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	open := func(initial bool) rsi.Host {
		t.Helper()
		if current != nil {
			if err := current.Close(); err != nil {
				t.Fatal(err)
			}
			current = nil
		}
		a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
		if err != nil {
			t.Fatal(err)
		}
		current = a
		// Only external protocol endpoint and trusted test carrier are injected.
		// Graph, Registry, Actor, bridge, JavaScript and receipts remain real.
		a.connectedApps.Client.Endpoint = broker.URL
		host := a.Plugins()
		host.Catalog.Launcher = carrier
		// The package test process is not the production launcher. All account
		// consumers use the same real carrier as the Actor, including after reopen.
		a.connectedApps.ReadInventory = func(c context.Context) (connectedapps.AccountInventoryPolicy, error) {
			return a.appsAccountInventoryWithHost(c, host)
		}
		a.connectedApps.ReadSettings = func(c context.Context) (connectedapps.SettingsPolicy, error) {
			return a.appsSettingsWithHost(c, host)
		}
		a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
			return a.appsAccountPoliciesWithHost(c, host)
		}
		a.connectedApps.ReadEditorial = func(c context.Context, q directorycontract.Query) (directorycontract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			return a.readAppsEditorialWithHost(c, host, q)
		}
		if initial {
			value, err := prepareWebDefault(ctx, host, cfg, directories.Extract)
			if err != nil {
				t.Fatal(err)
			}
			choice := directories
			if mode != "managed33" {
				choice.MemoryInspector = ""
			}
			if mode != "managed33" && mode != "managed32" {
				// Explicit historical compositions predate the UI and Schedule packages.
				choice.UI, choice.Schedule = "", ""
			}
			if !modern {
				choice.Artifact = ""
			}
			if !modern && mode != "prior29" {
				choice.GitHub = ""
			}
			if !modern && mode != "prior29" && mode != "prior28" {
				choice.AppsDirectory, choice.AppsAccounts, choice.AppsLogos, choice.AppsInterface = "", "", "", ""
			}
			if !discovery {
				choice.Discovery = ""
			}
			if !managed {
				// Actual prior default22 package graph/policy, selected through
				// owner verification before the new distribution's startup.
				choice.Apps = ""
			}
			if err := builtin.EnsureDefault(ctx, host, choice, value); err != nil {
				t.Fatal(err)
			}
			auth, err := a.LoginKey(ctx, []byte("synthetic-model-key"))
			if err != nil {
				t.Fatal(err)
			}
			profile, err := a.Models().AddProfile(ctx, llm.ProfileInput{Name: "Apps default workload", ProviderKind: "openai_compatible", WireAPI: "openai_completions", Endpoint: model.URL + "/v1", ModelID: "fixture-model", AuthConnectionID: auth.ConnectionID, ContextWindow: 65536, MaxOutputTokens: 4096, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ToolCalls: true, Streaming: true}})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Models().RecordProbe(ctx, profile, llm.ProbeRecord{ProfileVersion: profile.Version, AuthVersion: auth.Version, BridgeIdentity: a.control.BridgeIdentity, Result: llm.ProbeResult{Streaming: true, ToolCalls: true, Continuation: true, Cancellation: true, Clipping: true}}); err != nil {
				t.Fatal(err)
			}
			diagnostic, err := a.Diagnostics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.control.Select(ctx, profile.ProfileID, profile.Version, diagnostic.ConfigVersion); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.prepareCore(ctx); err != nil {
			t.Fatal(err)
		}
		// Match local CLI control/work composition: direct Actor work has no
		// broker phase. Source authorization and original receipts still apply.
		a.eventsRequired.Store(false)
		a.runtimeRuns().ConfigurePlugins(host.Catalog)
		return host
	}
	query := func(fn func(*sql.Tx)) {
		t.Helper()
		db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state.db")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fn(tx)
	}
	host := open(true)
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, selected.Revision)
	wantPackages := map[string]int{"managed33": 33, "managed32": 32, "prior30": 30, "prior29": 29, "prior28": 28, "prior24": 24, "prior23": 23, "prior22": 22}[mode]
	if err != nil || len(graph.Graph.Bindings) != wantPackages {
		t.Fatal("default Apps test used a partial distribution", graph, err)
	}
	if brokerCalls.Load() != 0 {
		t.Fatal("default verification accessed account service before explicit Connect")
	}
	old, err := current.Actor(nil).Submit(ctx, "apps-default-before-connect", "APPS_PLUGIN_NO_ACCOUNT")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.connectedApps.Connect(ctx, "gmail", "personal", ""); err != nil {
		t.Fatal("default account Connect", err)
	}
	state, err := current.connectedApps.State(ctx)
	if err != nil || len(state.Connections) != 1 {
		t.Fatal(state, err)
	}
	if err := current.connectedApps.Check(ctx, state.Connections[0].ID); err != nil {
		t.Fatal(err)
	}
	assertDefaultAppsPresentation(t, ctx, current, secrets, host, selected.Revision, modern || mode == "prior29" || mode == "prior28")
	name := "mcp__apps__b__" + strings.ReplaceAll(state.Connections[0].ID, "-", "") + "__"
	if err := current.Actor(nil).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	query(func(tx *sql.Tx) {
		var raw string
		if err := tx.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, old.RunID, codemode.ToolName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var outputs []struct {
			Value []string `json:"value"`
		}
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &outputs) != nil || len(outputs) != 1 || len(outputs[0].Value) != 0 {
			t.Fatal("already queued Main gained the new connection", raw)
		}
	})
	read := func(key string, unavailable bool) {
		t.Helper()
		receipt, err := current.Actor(nil).Submit(ctx, key, "APPS_PLUGIN_SEARCH")
		if err != nil {
			t.Fatal(err)
		}
		if err := current.Actor(nil).Drain(ctx); err != nil {
			t.Fatal(err)
		}
		query(func(tx *sql.Tx) {
			var raw, services, revision string
			if err := tx.QueryRow(`SELECT i.result_json,p.managed_services_json,p.graph_revision FROM tool_invocation i JOIN rsi_run p USING(run_id) WHERE i.run_id=? AND i.tool_name=? AND i.parent_invocation_id IS NOT NULL`, receipt.RunID, name+"search_tools").Scan(&raw, &services, &revision); err != nil {
				t.Fatal(err)
			}
			var result tool.Result
			if json.Unmarshal([]byte(raw), &result) != nil {
				t.Fatal(raw)
			}
			if unavailable {
				if result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
					t.Fatal("removed method restored native", raw)
				}
			} else {
				if result.Error != nil || !strings.Contains(result.Text, operation.Slug) || strings.Contains(services, "steward.apps-method/1") != managed || strings.Contains(services, "steward.apps-guidance/1") != managed || revision != selected.Revision {
					t.Fatal("real App did not dispatch original managed account source", raw, services, revision)
				}
				if strings.Contains(services, "steward.tool-discovery/1") != discovery {
					t.Fatal("actual current/prior discovery boundary drifted", mode, services)
				}
				var ranked int
				if err := tx.QueryRow(`SELECT count(*) FROM code_search_reply c JOIN code_manifest m USING(execution_id) JOIN tool_invocation owner ON owner.invocation_id=m.execution_id WHERE owner.run_id=?`, receipt.RunID).Scan(&ranked); err != nil {
					t.Fatal(err)
				}
				if discovery && ranked != 1 || !discovery && ranked != 0 {
					t.Fatal("actual full composition bypassed rank service", mode, ranked)
				}

			}
			if !unavailable {
				if err := tx.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, receipt.RunID, codemode.ToolName).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(raw, "Connected app account: Gmail / personal") {
					t.Fatal("production CodeMode did not discover the frozen account namespace", raw)
				}
			}
		})
	}
	read("apps-default-first", false)
	host = open(false)
	read("apps-default-cold", false)
	if after, err := host.Store.Status(ctx); err != nil || after != selected {
		t.Fatal("cold runtime replaced owner selection", after, selected, err)
	}
	receipt, err := current.Actor(nil).Submit(ctx, "apps-default-effect", "APPS_PLUGIN_EFFECT")
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Actor(nil).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	approvals, err := current.Approvals(ctx)
	if err != nil || len(approvals) != 1 || approvals[0].Tool != name+"execute" || effects.Load() != 0 {
		t.Fatal("real App bypassed original exact approval", approvals, effects.Load(), err)
	}
	host = open(false)
	if err := current.DecideApproval(ctx, approvals[0].ID, approvals[0].Hash, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := current.Actor(nil).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	host = open(false)
	if err := current.Actor(nil).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if effects.Load() != 1 {
		t.Fatal("cold approval recovery repeated or dropped original POST", effects.Load())
	}
	query(func(tx *sql.Tx) {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='unknown' AND replay_policy='never' AND attempt_count=1`, receipt.RunID, name+"execute").Scan(&count); err != nil || count != 1 {
			t.Fatal("original unknown effect receipt changed", count, err)
		}
		if err := tx.QueryRow(`SELECT count(*) FROM rsi_call WHERE instance_id='connected-apps'`).Scan(&count); err != nil || count != 0 {
			t.Fatal("default Apps created a second effect ledger", count, err)
		}
	})
	if !managed {
		if after, err := host.Store.Status(ctx); err != nil || after != selected {
			t.Fatal("actual prior22 account work was silently opted into Apps", after, selected, err)
		}
		t.Log("actual prior22 owner graph retained native method/guidance across connected-account reads, cold exact approval and unknown recovery")
		return
	}
	// Remove only this atomic tool feature. Existing owner connection controls
	// and every other independently selected business package remain available.
	bindings := []rsiplugin.Binding{}
	for _, binding := range graph.Graph.Bindings {
		if binding.InstanceID != "connected-apps" {
			bindings = append(bindings, binding)
		}
	}
	target, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	var policyID string
	query(func(tx *sql.Tx) {
		if err := tx.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, selected.Revision).Scan(&policyID); err != nil {
			t.Fatal(err)
		}
	})
	policy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "owner-without-app-tools"
	cases := policy.Cases[:0]
	for _, item := range policy.Cases {
		if item.InstanceID != "connected-apps" {
			cases = append(cases, item)
		}
	}
	policy.Cases = cases
	contracts := policy.AllowedContracts[:0]
	for _, item := range policy.AllowedContracts {
		if item != "steward.apps-method/1" && item != "steward.apps-guidance/1" {
			contracts = append(contracts, item)
		}
	}
	policy.AllowedContracts = contracts
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	const change = "remove-app-tools"
	if err := host.Propose(ctx, rsi.Candidate{ID: change, Baseline: selected.Revision, Target: target.Revision, PolicyID: policy.ID, Goal: "Disable account tools while retaining independent owner controls"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: change, Expected: selected.Revision, Target: target.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	host = open(false)
	read("apps-default-disabled", true)
	if err := current.connectedApps.Check(ctx, state.Connections[0].ID); err != nil {
		t.Fatal("disabling tool feature disabled independent owner account control", err)
	}
	if after, err := host.Store.Status(ctx); err != nil || after.Revision != target.Revision {
		t.Fatal("cold runtime repaired disabled tool selection", after, err)
	}
	modelMu.Lock()
	defer modelMu.Unlock()
	if len(modelBodies) < 8 {
		t.Fatal("real provider bridge/continuation was not exercised", mode, len(modelBodies))
	}
	t.Logf("%s actual App/bridge/CodeMode: queued scope, first/cold reads, cold exact approval, unknown non-replay, independent removal and owner Check passed; %d model requests", mode, len(modelBodies))
}
