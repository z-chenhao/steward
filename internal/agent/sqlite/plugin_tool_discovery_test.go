package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestToolDiscoveryOriginal288FrozenRankings(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/tool-discovery-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Catalogs [][]tool.Spec
		Cases    []struct {
			Catalog  int
			Loaded   []string
			Query    string
			Limit    int
			Expected tool.SearchResult
		}
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil || len(fixtures.Cases) != 288 {
		t.Fatal(err, len(fixtures.Cases))
	}
	for i, c := range fixtures.Cases {
		pool, metadata := toolDiscoveryPool(fixtures.Catalogs[c.Catalog], c.Loaded)
		f := contract.Facts{Query: c.Query, Candidates: metadata}
		encoded := contract.Encode(f)
		decoded, err := contract.Decode(encoded, len(metadata))
		if err != nil || !reflect.DeepEqual(decoded, f) {
			t.Fatal("metadata changed", i, err)
		}
		plan := tooldiscovery.Rank(decoded, c.Limit)
		got, err := materializeToolDiscovery(pool, plan, max(1, c.Limit))
		if err != nil || !reflect.DeepEqual(got, c.Expected) {
			t.Fatalf("original case %d changed: got=%#v want=%#v err=%v", i, got, c.Expected, err)
		}
	}
}
func TestToolDiscoveryHostRejectsForgedIndicesSpansAndDuplicateActivation(t *testing.T) {
	pool, _ := toolDiscoveryPool([]tool.Spec{{Name: "authorized", Description: "original", Exposure: tool.Deferred}, {Name: "hidden", Description: "private", Exposure: tool.Hidden}, {Name: "internal", Exposure: tool.Deferred, Internal: true}, {Name: "already-loaded", Exposure: tool.Deferred}}, []string{"already-loaded"})
	if len(pool) != 1 {
		t.Fatal("authority leaked before plugin", pool)
	}
	for _, entries := range [][]contract.Entry{
		{{Index: 1}}, {{Index: -1}}, {{Index: 0, Offset: 9}}, {{Index: 0, Offset: -1}}, {{Index: 0, Length: -1}}, {{Index: 0, Offset: 4, Length: int(^uint(0) >> 1)}}, {{Index: 0}, {Index: 0}},
	} {
		if _, err := materializeToolDiscovery(pool, contract.Plan{Entries: entries}, 8); err == nil {
			t.Fatal("forgery accepted", entries)
		}
	}
	original := pool[0].Description
	pool[0].Description = "中文"
	if _, err := materializeToolDiscovery(pool, contract.Plan{Entries: []contract.Entry{{Index: 0, Offset: 1, Length: 2}}}, 1); err == nil {
		t.Fatal("mid-rune excerpt invented replacement text")
	}
	pool[0].Description = original
	pool[0].CodeModeOnly = true
	got, err := materializeToolDiscovery(pool, contract.Plan{Entries: []contract.Entry{{Index: 0, Length: 8}}}, 1)
	if err != nil || len(got.Loaded) != 0 || len(got.Tools) != 1 || got.Tools[0].Description != "original" {
		t.Fatal("CodeMode-only became model activation", got, err)
	}
}

func installToolDiscoveryPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	facts := contract.Facts{Query: "shared", Candidates: []contract.Candidate{{Name: "a", Description: "shared"}}}
	return installRuntimeFeature(t, s, r, "tool-discovery", rsi.Policy{ID: "discovery-owner-fixture", AllowedContracts: []string{contract.Contract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "tool-discovery", Contract: contract.Contract, Input: json.RawMessage(`{"count":1,"limit":1}`), Inputs: []rsi.InputData{{ID: contract.InputID, Data: contract.Encode(facts)}}, Expected: json.RawMessage(`{"entries":[{"index":0,"offset":0,"length":6,"ellipsis":false}]}`)}}})
}
func TestToolDiscoveryActualNative500AndWorkerCodeSearchWithoutGrantingAccess(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.1.0"} {
		t.Run(version, func(t *testing.T) {
			target := "mcp__catalog__lookup_000"
			if version == "1.1.0" {
				target = "mcp__catalog__lookup_499"
			}
			testToolDiscoveryMainAndWorker(t, func(s *Store, r *Runs) {
				h := installToolDiscoveryPlugin(t, s, r)
				if version != "1.0.0" {
					status, err := h.Store.Status(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					selectToolDiscoveryVersion(t, h, status.Revision, version)
				}
			}, "catalog", target)
		})
	}
}

func toolDiscoveryCodeFixture(t *testing.T, versions ...string) (*Store, *Runs, *codeBridge, tool.Invocation, rsi.Host) {
	t.Helper()
	ctx := context.Background()
	var h rsi.Host
	defs := largeToolCatalog()
	calls := []llm.ToolCall{{ID: "code", Name: codemode.ToolName, Arguments: `{"source":"return 1;"}`}}
	s, r, _, executor, exchange := admitPolicyRun(t, defs, calls, false, func(s *Store, r *Runs) {
		h = installToolDiscoveryPlugin(t, s, r)
		if len(versions) > 0 {
			status, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectToolDiscoveryVersion(t, h, status.Revision, versions[0])
		}
	})
	invs, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	b := &codeBridge{t: executor.Store.(*tools), registry: executor.Registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: invs[0].ID, ParentInvocationID: invs[0].ID, RunID: executor.Store.(*tools).claim.RunID(), Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
	if err := b.claim(ctx, invs[0]); err != nil {
		t.Fatal(err)
	}
	return s, r, b, invs[0], h
}
func TestToolDiscoveryActualAcceptedCodeSearchCachesBeforeAck(t *testing.T) {
	s, _, b, _, _ := toolDiscoveryCodeFixture(t)
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["lookup_499",{"limit":1,"namespace":"mcp__catalog"}]`)
	reply, err := b.Handle(context.Background(), "host.discover", m)
	if err != nil || reply.Error != nil {
		t.Fatal("accepted discovery failed", reply, err)
	}
	raw, _ := encoded(reply)
	if !strings.Contains(raw, "mcp__catalog__lookup_499") || scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 1 {
		t.Fatal("search not durably versioned", raw)
	}
}

func toolDiscoveryVersionPackage(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	replacements := map[string]string{}
	for _, name := range []string{"plugin.go", "rank.go"} {
		path, err := filepath.Abs("../../plugin/tooldiscovery/" + name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		if name == "plugin.go" {
			source = strings.Replace(source, `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
		}
		if name == "plugin.go" && version == "1.2.0" {
			source = strings.Replace(source, `"context"`, `"context";"os";"time"`, 1)
			marker := "return rsiplugin.JSONResult(ctx, Rank(f, req.Limit))"
			if !strings.Contains(source, marker) {
				t.Fatal("actual rank return missing")
			}
			source = strings.Replace(source, marker, `if f.Query=="catalog" {if err:=os.WriteFile("rank-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for {if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};`+marker, 1)
		}
		if name == "plugin.go" && version == "1.3.0" {
			marker := "return rsiplugin.JSONResult(ctx, Rank(f, req.Limit))"
			if !strings.Contains(source, marker) {
				t.Fatal("rank result marker absent")
			}
			source = strings.Replace(source, marker, `if f.Query=="catalog"{return rsiplugin.JSONResult(ctx,contract.Plan{Entries:[]contract.Entry{{Index:len(f.Candidates)}}})};`+marker, 1)
		}
		if name == "rank.go" && version == "1.1.0" {
			old := "return a.spec.Name < b.spec.Name"
			if !strings.Contains(source, old) {
				t.Fatal("actual rank comparator absent")
			}
			source = strings.Replace(source, old, "return a.spec.Name > b.spec.Name", 1)
		}
		replacement := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(replacement, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = replacement
	}
	raw, _ := json.Marshal(map[string]any{"Replace": replacements})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../../cmd/plugins/tool-discovery").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	raw, err := os.ReadFile("../../../plugins/tool-discovery/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	m, err = rsi.Seal(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func selectToolDiscoveryVersion(t *testing.T, h rsi.Host, baseline, version string) string {
	t.Helper()
	ctx := context.Background()
	p, err := h.Install(ctx, toolDiscoveryVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "tool-discovery", PackageDigest: p.Digest, Config: json.RawMessage(`{}`), StateNamespace: "tool-discovery", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "discovery-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "discovery-owner-" + version
	if err := h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "discovery-upgrade-"+version, baseline, g.Revision, policy.ID)
	return g.Revision
}

func TestToolDiscoveryAcceptedCacheQuarantineAndOldVMFences(t *testing.T) {
	ctx := context.Background()
	s, r, b, _, h := toolDiscoveryCodeFixture(t)
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["catalog",{"limit":1}]`)
	first, err := b.Handle(ctx, "host.discover", m)
	if err != nil || first.Error != nil {
		t.Fatal(first, err)
	}
	before, _ := encoded(first)
	for _, sql := range []string{`UPDATE code_search_reply SET reply_json='{}'`, `DELETE FROM code_search_reply`, `INSERT OR REPLACE INTO code_search_reply SELECT execution_id,request_hash,'{}' FROM code_search_reply`} {
		if _, err := s.db.Exec(sql); err == nil {
			t.Fatal("accepted cache mutable", sql)
		}
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	same, err := b.Handle(ctx, "host.discover", m)
	after, _ := encoded(same)
	if err != nil || before != after {
		t.Fatal("accepted reply changed after quarantine", same, err)
	}
	m.Sequence++
	m.Arguments = json.RawMessage(`["lookup_499",{"limit":1}]`)
	denial, err := b.Handle(ctx, "host.discover", m)
	if err != nil || denial.Error == nil || denial.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("new query bypassed quarantine", denial, err)
	}
	r.plugins = nil
	m.Sequence++
	replay, err := b.Handle(ctx, "host.discover", m)
	if err != nil || replay.Error == nil || replay.Error.Code != denial.Error.Code {
		t.Fatal("frozen denial changed without runner", replay, err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_call`) != 0 || scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 2 {
		t.Fatal("discovery dispatched tools or failed accepted replies")
	}
	if _, err := s.db.Exec(`UPDATE code_execution_state SET claim_expires_at=0,version=version+1`); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	if _, err := b.Handle(ctx, "host.discover", m); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old VM read accepted cache", err)
	}
}
func TestToolDiscoveryCacheCommitFailureNeverAcknowledgesUnretainedReply(t *testing.T) {
	ctx := context.Background()
	s, _, b, _, _ := toolDiscoveryCodeFixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_code_search BEFORE INSERT ON code_search_reply BEGIN SELECT RAISE(ABORT,'injected cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["catalog",{"limit":1}]`)
	if _, err := b.Handle(ctx, "host.discover", m); err == nil {
		t.Fatal("unretained reply acknowledged")
	}
	if scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 0 {
		t.Fatal("failed cache write leaked reply")
	}
}
func TestToolDiscoveryActualFrozenMetadataBeyondOrdinaryInputLimit(t *testing.T) {
	description := strings.Repeat("x", 9<<20)
	spec := tool.Spec{Name: "mcp__large__lookup", Description: description, Exposure: tool.Deferred, CodeModeOnly: true, Version: 1, InputSchema: `{"type":"object"}`, Permission: "read_only", ReplayPolicy: "safe"}
	defs := []tool.Definition{tool.SearchDefinition(), {Spec: spec, Execute: func(context.Context, string) tool.Result { t.Fatal("ranking executed candidate"); return tool.Result{} }}}
	s, r, a, p := childFixture(t, defs...)
	installToolDiscoveryPlugin(t, s, r)
	expected, _ := encoded(tool.SearchToolsLegacyV1([]tool.Spec{spec}, nil, "lookup", 1))
	observed := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			if last.Text != expected {
				t.Fatal("original large metadata changed", len(last.Text), len(expected))
			}
			observed = true
			return llm.GenerationResult{Text: "observed original large metadata"}, nil
		}
		return calls(tool.SearchName, `{"query":"lookup","limit":1}`), nil
	}
	mustSubmit(t, a, "large-discovery-metadata", "Find an authorized large metadata entry")
	// Bound the complete large-fixture workflow;the actual native search still
	// has its original10s policy and uses real Actor lease renewals.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := a.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("large metadata never consumed actual discovery")
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND json_extract(policy_json,'$.timeout_ms')=10000 AND status='succeeded'`, tool.SearchName) != 1 {
		t.Fatal("large metadata changed original10s tool policy or never succeeded")
	}
}

func TestToolDiscoveryLateForgetRejectsReplyWithoutHoldingSQLiteWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s, r, b, _, _ := toolDiscoveryCodeFixture(t, "1.2.0")
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["catalog",{"limit":1}]`)
	done := make(chan struct{})
	var readErr error
	go func() { defer close(done); _, readErr = b.Handle(ctx, "host.discover", m) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("query exited before privacy boundary", readErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "rank-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("pure search held SQLite writer or blocked Forget", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(readErr, agent.ErrClaimLost) || scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 0 || scalar(t, s, `SELECT count(*) FROM code_call`) != 0 {
		t.Fatal("late private result acknowledged or tools invoked", readErr)
	}
}

func TestToolDiscoveryActualFrozenRootUpgradeRollbackRetireAndDisable(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, largeToolCatalog()...)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	r.ConfigureJavaScript(realCodeRuntime(t))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		return calls(codemode.ToolName, `{"source":"const found=await searchTools('catalog',{limit:1});return found[0]?.name??'EMPTY';"}`), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Search authorized catalog tools")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id, target string, managed bool) {
		t.Helper()
		var result string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, codemode.ToolName).Scan(&result); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result, target) {
			t.Fatal("frozen ranking changed", id, result, target)
		}
		n := scalar(t, s, `SELECT count(*) FROM code_search_reply q JOIN code_manifest m USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?`, id)
		if managed && n != 1 || !managed && n != 0 {
			t.Fatal("frozen service boundary changed", id, n, managed)
		}
	}
	legacy := queue("search-native")
	h := installToolDiscoveryPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(legacy, "mcp__catalog__lookup_000", false)
	old := queue("search-old")
	second := selectToolDiscoveryVersion(t, h, first.Revision, "1.1.0")
	mustDrain(t, a)
	check(old, "mcp__catalog__lookup_000", true)
	current := queue("search-new")
	mustDrain(t, a)
	check(current, "mcp__catalog__lookup_499", true)
	retained := queue("search-retained")
	activateDelegation(t, h, "search-rollback", second, first.Revision, "discovery-owner-fixture")
	graph, err := h.Store.Graph(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Retire(ctx, graph.Graph.Bindings[0].PackageDigest); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(retained, "mcp__catalog__lookup_499", true)
	rolled := queue("search-rolled")
	mustDrain(t, a)
	check(rolled, "mcp__catalog__lookup_000", true)
	if _, err := h.Store.Deactivate(ctx, "search-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := queue("search-disabled")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM code_search_reply q JOIN code_manifest m USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=? AND json_extract(q.reply_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 {
		t.Fatal("deactivation restored native ranking")
	}
}

func TestToolDiscoveryRealVMRepliesStayByteStableAcrossNestedSuspension(t *testing.T) {
	s, r, a, p := childFixture(t, largeToolCatalog()...)
	installToolDiscoveryPlugin(t, s, r)
	r.ConfigureJavaScript(realCodeRuntime(t))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		source := `const a=await searchTools('catalog',{limit:1});const b=await searchTools('catalog',{limit:1});const x=await tools.mcp__catalog__lookup_000({text:'nested'});const c=await searchTools('catalog',{limit:1});return {before:JSON.stringify(a),again:JSON.stringify(b),after:JSON.stringify(c),value:x};`
		raw, _ := json.Marshal(map[string]string{"source": source})
		return calls(codemode.ToolName, string(raw)), nil
	})
	mustSubmit(t, a, "stable-search-replay", "Search and execute one authorized tool")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM code_search_reply`) != 1 || scalar(t, s, `SELECT count(*) FROM code_call`) != 1 || scalar(t, s, `SELECT count(*) FROM code_event WHERE json_extract(message_json,'$.toolName')='searchTools'`) < 3 {
		t.Fatal("real nested suspension/search replay absent")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, codemode.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var output []struct {
		Type  string                                `json:"type"`
		Value struct{ Before, Again, After string } `json:"value"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &output) != nil || len(output) != 1 || output[0].Value.Before == "" || output[0].Value.Before != output[0].Value.Again || output[0].Value.Before != output[0].Value.After {
		t.Fatal("search changed accepted bytes after VM reconstruction", raw)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='mcp__catalog__lookup_000' AND attempt_count=1 AND status='succeeded'`) != 1 {
		t.Fatal("nested tool replayed")
	}
}
func TestToolDiscoveryActualColdVMRecoversFrozenReplyAndOriginalRankVersion(t *testing.T) {
	ctx := context.Background()
	s, r, b, inv, h := toolDiscoveryCodeFixture(t)
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["catalog",{"limit":1}]`)
	first, err := b.Handle(ctx, "host.discover", m)
	if err != nil || first.Error != nil {
		t.Fatal(first, err)
	}
	before, _ := encoded(first)
	var manifest string
	if err := s.db.QueryRow(`SELECT request_json FROM code_manifest WHERE execution_id=?`, b.req.ExecutionID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectToolDiscoveryVersion(t, h, status.Revision, "1.1.0")
	wake, err := b.wake(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idle, err := b.release(ctx, wake); err != nil || !idle {
		t.Fatal("original VM not suspended", idle, err)
	}
	data := filepath.Dir(s.path)
	catalog := h.Catalog
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	r.host = next
	r.ConfigurePlugins(catalog)
	if err := b.t.Start(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	restored := &codeBridge{t: b.t, req: b.req, attempt: newID(), registry: b.registry}
	restored.req.ClaimToken = ""
	restored.req.ExecutionEpoch = 0
	if err := restored.claim(ctx, inv); err != nil {
		t.Fatal(err)
	}
	replay := codeMessage(restored)
	replay.ToolName = "searchTools"
	replay.Arguments = m.Arguments
	result, err := restored.Handle(ctx, "host.discover", replay)
	after, _ := encoded(result)
	if err != nil || before != after || restored.req.ExecutionEpoch != b.req.ExecutionEpoch+1 {
		t.Fatal("cold reply drifted", result, err)
	}
	replay.Sequence++
	replay.Arguments = json.RawMessage(`["record",{"limit":1}]`)
	original, err := restored.Handle(ctx, "host.discover", replay)
	raw, _ := encoded(original)
	if err != nil || original.Error != nil || !strings.Contains(raw, "mcp__catalog__lookup_000") {
		t.Fatal("cold uncached query used current1.1 instead of original1.0", original, err)
	}
	var unchanged string
	if err := next.db.QueryRow(`SELECT request_json FROM code_manifest WHERE execution_id=?`, b.req.ExecutionID).Scan(&unchanged); err != nil || unchanged != manifest {
		t.Fatal("cold recovery rewrote original code manifest", err)
	}
	m.Sequence++
	if _, err := b.Handle(ctx, "host.discover", m); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("pre-cold VM still admitted", err)
	}
}

func TestToolDiscoveryDeclaredBadPlanCannotRestoreNativeOrLoadForeignTools(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, largeToolCatalog()...)
	h := installToolDiscoveryPlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectToolDiscoveryVersion(t, h, status.Revision, "1.3.0")
	observed := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "tool" {
			if !strings.Contains(last.Text, "PROVIDER_UNAVAILABLE") {
				t.Fatal("native guard did not report unavailable", last.Text)
			}
			observed = true
			return llm.GenerationResult{Text: "observed guard"}, nil
		}
		return calls(tool.SearchName, `{"query":"catalog","limit":1}`), nil
	}
	receipt := mustSubmit(t, a, "bad-discovery-plan", "Search authorized metadata")
	mustDrain(t, a)
	var checkpoint string
	if err := s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, receipt.RunID).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	var cp agent.Checkpoint
	if !observed || json.Unmarshal([]byte(checkpoint), &cp) != nil || len(cp.LoadedTools) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name LIKE 'mcp__catalog__%'`) != 0 {
		t.Fatal("bad candidate loaded or executed tools", observed, checkpoint)
	}
	cs, _, b, _, _ := toolDiscoveryCodeFixture(t, "1.3.0")
	m := codeMessage(b)
	m.ToolName = "searchTools"
	m.Arguments = json.RawMessage(`["catalog",{"limit":1}]`)
	denial, err := b.Handle(ctx, "host.discover", m)
	if err != nil || denial.Error == nil || denial.Error.Code != "PROVIDER_UNAVAILABLE" || scalar(t, cs, `SELECT count(*) FROM code_call`) != 0 {
		t.Fatal("CodeMode bad plan restored native or dispatched", denial, err)
	}
}

func TestToolDiscoveryV65MigrationKeepsNativeFrozenManifestAndNoOptIn(t *testing.T) {
	ctx := context.Background()
	s, _, b, _ := codeClaimFixture(t)
	snapshot := func(store *Store) string {
		t.Helper()
		var raw string
		if err := store.db.QueryRow(`SELECT json_object('request',m.request_json,'hash',m.manifest_hash,'state',s.state,'epoch',s.execution_epoch,'token',s.claim_token,'graph',r.graph_revision,'services',r.managed_services_json) FROM code_manifest m JOIN code_execution_state s USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id JOIN rsi_run r ON r.run_id=i.run_id WHERE m.execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot(s)
	if _, err := s.db.Exec(removeCodeSearchFixtureSchema + `PRAGMA user_version=65`); err != nil {
		t.Fatal(err)
	}
	data := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM code_search_reply`) != 0 || scalar(t, next, `SELECT count(*) FROM rsi_run b,json_each(b.managed_services_json) s WHERE b.run_id=? AND s.value=?`, b.req.RunID, contract.Contract) != 0 {
		t.Fatal("forward migration rewrote/opted in native execution")
	}
}

func TestToolDiscoveryNativeChunkFenceKeepsMutableAuthorityAndImmutableProof(t *testing.T) {
	ctx := context.Background()
	calls := []llm.ToolCall{{ID: "search", Name: tool.SearchName, Arguments: `{"query":"lookup","limit":1}`}}
	s, r, _, executor, exchange := admitPolicyRun(t, largeToolCatalog(), calls, false)
	invs, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Store.Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	native := executor.Store.(*tools)
	if err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { _, _, _, err := native.acceptedBuiltin(ctx, tx, invs[0]); return err }); err != nil {
		t.Fatal("initial immutable capability proof", err)
	}
	if err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { return native.toolDiscoveryAcceptedFence(ctx, tx, invs[0]) }); err != nil {
		t.Fatal("live admitted callback denied", err)
	}
	replacement := newID()
	if _, err := s.db.Exec(`INSERT INTO capability_snapshot(snapshot_id,agent_config_version,role,tool_specs_hash,skill_set_hash,data_scope,can_spawn_agents,tools_json,created_at,format_version) SELECT ?,agent_config_version,role,tool_specs_hash,skill_set_hash,data_scope,can_spawn_agents,tools_json,created_at,format_version FROM capability_snapshot WHERE snapshot_id=(SELECT capability_snapshot_id FROM run WHERE run_id=?)`, replacement, native.claim.RunID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run SET capability_snapshot_id=?,version=version+1 WHERE run_id=?`, replacement, native.claim.RunID()); err == nil {
		t.Fatal("frozen reference could change to another valid snapshot")
	}
	for _, sql := range []string{`UPDATE capability_snapshot SET tools_json='[]'`, `UPDATE tool_invocation SET arguments_json='{}',version=version+1`} {
		if _, err := s.db.Exec(sql); err == nil {
			t.Fatal("immutable proof became mutable", sql)
		}
	}
	if err := executor.Store.Finish(ctx, invs[0].ID, tool.Result{Text: `{"loaded":[],"tools":[]}`}); err != nil {
		t.Fatal(err)
	}
	if err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { return native.toolDiscoveryAcceptedFence(ctx, tx, invs[0]) }); err == nil {
		t.Fatal("callback retained authority after invocation finished")
	}
}
func TestToolDiscoveryNativeLateForgetRejectsStreamedReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	calls := []llm.ToolCall{{ID: "search", Name: tool.SearchName, Arguments: `{"query":"catalog","limit":1}`}}
	s, r, _, executor, exchange := admitPolicyRun(t, largeToolCatalog(), calls, false, func(s *Store, r *Runs) {
		h := installToolDiscoveryPlugin(t, s, r)
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectToolDiscoveryVersion(t, h, status.Revision, "1.2.0")
	})
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	done := make(chan struct{})
	var callErr error
	go func() { defer close(done); _, callErr = executor.Execute(ctx, exchange, calls) }()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	marker := ""
wait:
	for {
		select {
		case <-done:
			t.Fatal("native query exited before privacy boundary", callErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "rank-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("native query blocked Forget", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(callErr, agent.ErrClaimLost) || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded'`, tool.SearchName) != 0 {
		t.Fatal("native result survived revocation", callErr)
	}
}
