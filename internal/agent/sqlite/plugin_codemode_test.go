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
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/codemodepolicy/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func installCodeModePlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	return installRuntimeFeature(t, s, r, "codemode", rsi.Policy{ID: "codemode-owner-fixture", AllowedContracts: []string{contract.LimitsContract, contract.FormatContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "codemode", Contract: contract.LimitsContract, Input: json.RawMessage(`{"role":"main","ceilings":{"timeout_ms":30000,"memory_bytes":33554432,"output_bytes":65536,"pending_calls":128,"interrupt_budget":100000}}`), Expected: json.RawMessage(`{"limits":{"timeout_ms":30000,"memory_bytes":33554432,"output_bytes":65536,"pending_calls":128,"interrupt_budget":100000}}`)},
		{InstanceID: "codemode", Contract: contract.FormatContract, Input: json.RawMessage(`{"input_id":"schema"}`), Inputs: []rsi.InputData{{ID: "schema", Data: []byte(`{"type":"object","properties":{"q":{"type":"string"},"count":{"type":"integer"}},"required":["q"],"additionalProperties":false}`)}}, Expected: json.RawMessage(`{"input_type":"{ \"count\"?: number; \"q\": string }","guidance":""}`)},
	}})
}

func codeModeVersionPackage(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/codemodepolicy/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
	if version == "1.1.0" {
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.LimitsPlan`, `f.Ceilings.PendingCalls=min(f.Ceilings.PendingCalls,64);return rsiplugin.JSONResult(ctx, contract.LimitsPlan`, 1)
		code = strings.Replace(code, `InputType: inputType(schema), Guidance: ""`, `InputType: "Readonly<"+inputType(schema)+">", Guidance: "VERSION_1_1_CODEMODE"`, 1)
	} else if version == "1.2.0" {
		code = strings.Replace(code, `return rsiplugin.JSONResult(ctx, contract.LimitsPlan`, `f.Ceilings.PendingCalls++;return rsiplugin.JSONResult(ctx, contract.LimitsPlan`, 1)
	} else if version == "1.3.0" {
		code = strings.Replace(code, `"context"`, `"context";"strings";"os";"time"`, 1)
		code = strings.Replace(code, `var schema any`, `if strings.Contains(string(raw),"text") {if err:=os.WriteFile("format-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case<-ctx.Done():return rsiplugin.Result{},ctx.Err();case<-time.After(time.Millisecond):}}};var schema any`, 1)
	}
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/codemode").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/codemode/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	if version == "1.2.0" {
		// A candidate may broaden its own schema. The owner still enforces the
		// original execution ceiling independently of that declaration.
		compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(string(m.Provides[0].OutputSchema))
		m.Provides[0].OutputSchema = json.RawMessage(strings.Replace(compact, `"maximum":128`, `"maximum":129`, 1))
	}
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestCodeModePluginKeepsMainAndWorkerRoleBoundaries(t *testing.T) {
	testCodeModeRoles(t, false, func(s *Store, r *Runs) { installCodeModePlugin(t, s, r) })
}

func TestCodeModePluginMissingRunnerCannotBypassDurableGuardian(t *testing.T) {
	for _, control := range []string{"quarantine", "stop"} {
		t.Run(control, func(t *testing.T) {
			s, r, b, _ := codeClaimFixtureMode(t, true)
			ctx := context.Background()
			h := r.pluginHost(b.t.claim)
			status, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if control == "stop" {
				err = h.Store.Stop(ctx, true)
			} else {
				err = h.Store.Quarantine(ctx, status.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			// An embedder can recover an already managed store without wiring
			// an executable catalog. Persisted Host authority still applies.
			r.plugins = nil
			if reply, err := b.Handle(ctx, "host.invoke", codeMessage(b)); !errors.Is(err, rsi.ErrRevoked) {
				t.Fatal("missing runner bypassed durable Guardian", reply, err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE parent_invocation_id IS NOT NULL`) != 0 || scalar(t, s, `SELECT count(*) FROM code_call`) != 0 {
				t.Fatal("blocked work accepted without runner")
			}
		})
	}
}

func TestCodeModePluginQuarantineBetweenPolicyAndManifestRejectsAcceptance(t *testing.T) {
	s, r, b, inv := unclaimedCodeFixture(t, true)
	ctx := context.Background()
	if scalar(t, s, `SELECT count(*) FROM code_manifest`) != 0 {
		t.Fatal("fixture already accepted execution")
	}
	h := r.pluginHost(b.t.claim)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if err = b.claim(ctx, inv); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("policy bypassed the final Guardian fence", err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_execution`) != 0 || scalar(t, s, `SELECT count(*) FROM code_manifest`) != 0 {
		t.Fatal("quarantine partially accepted code state")
	}
}

func TestV58CodeDescriptionMigrationPreservesNativeManifestAndReplay(t *testing.T) {
	s, _, b, _ := codeClaimFixture(t)
	ctx := context.Background()
	snapshot := func(store *Store) string {
		t.Helper()
		var raw string
		if err := store.db.QueryRow(`SELECT json_object('request',m.request_json,'hash',m.manifest_hash,'source',m.source,'state',s.state,'epoch',s.execution_epoch,'token',s.claim_token) FROM code_manifest m JOIN code_execution_state s USING(execution_id) WHERE execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot(s)
	if _, err := s.db.Exec(removeComputerActionFixtureSchema + `DROP TABLE task_label_plan; DROP TABLE code_report_method_binding; DROP TABLE code_import_policy_binding; DROP TABLE shell_method_plan; DROP TABLE file_method_plan; DROP TABLE code_description_reply; PRAGMA user_version=58`); err != nil {
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
	if snapshot(next) != before || scalar(t, next, `SELECT count(*) FROM code_description_reply`) != 0 || scalar(t, next, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value LIKE 'steward.codemode-%'`, b.req.RunID) != 0 {
		t.Fatal("upgrade rewrote a historical execution or opted it in")
	}
}

func TestCodeModePluginLateForgetRejectsDescriptionBeforeAcknowledgment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var h rsi.Host
	echo, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
	calls := []llm.ToolCall{{ID: "code", Name: codemode.ToolName, Arguments: `{"source":"return 1;"}`}}
	s, r, _, e, exchange := admitPolicyRun(t, []tool.Definition{codemode.Definition(), echo}, calls, false, func(s *Store, r *Runs) {
		h = installCodeModePlugin(t, s, r)
		status, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectCodeModeVersion(t, h, status.Revision, "1.3.0")
	})
	invs, err := e.Store.Accept(ctx, exchange, calls, e.Registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Store.Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	b := &codeBridge{t: e.Store.(*tools), registry: e.Registry, attempt: newID(), req: codemode.ExecutionRequest{ProtocolVersion: codemode.ProtocolVersion, RuntimeVersion: codemode.RuntimeVersion, PreludeVersion: codemode.PreludeVersion, ExecutionID: invs[0].ID, ParentInvocationID: invs[0].ID, RunID: e.Store.(*tools).claim.RunID(), Source: "return 1;", SourceHash: digest("return 1;"), Limits: codemode.DefaultLimits()}}
	if err = b.t.prepareCodeLimits(ctx, invs[0], &b.req); err != nil {
		t.Fatal(err)
	}
	if err = b.claim(ctx, invs[0]); err != nil {
		t.Fatal(err)
	}
	owner := idAgent(t, s)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	m := codeMessage(b)
	m.ToolName = "describeTool"
	m.Arguments = json.RawMessage(`"mcp__fixture__echo"`)
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
			t.Fatal("formatter exited before privacy boundary", readErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "format-ready"))
			if len(paths) == 1 {
				marker = paths[0]
				break wait
			}
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal("formatter blocked Forget", err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !errors.Is(readErr, agent.ErrClaimLost) || scalar(t, s, `SELECT count(*) FROM code_description_reply`) != 0 || scalar(t, s, `SELECT count(*) FROM code_call`) != 0 {
		t.Fatal("late metadata accepted after Forget", readErr)
	}
}

func TestCodeModePluginCannotExpandHostLimitsBeforeVMOrNestedEffects(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, codemode.Definition())
	h := installCodeModePlugin(t, s, r)
	r.ConfigureJavaScript(realCodeRuntime(t))
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectCodeModeVersion(t, h, first.Revision, "1.2.0")
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed denied limits"}, nil
		}
		return calls(codemode.ToolName, `{"source":"return await tools.mcp__fixture__echo({text:'MUST_NOT_RUN'});"}`), nil
	})
	mustSubmit(t, a, "code-invalid-limits", "Execute bounded work")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM code_manifest`) != 0 || scalar(t, s, `SELECT count(*) FROM code_call`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, codemode.ToolName) != 1 {
		t.Fatal("candidate declaration expanded Host limits or performed work")
	}
}

func TestCodeModePluginFrozenDescriptionsSurviveQuarantineButNewWorkAndOldOwnersDoNot(t *testing.T) {
	s, r, b, inv := codeClaimFixtureMode(t, true)
	ctx := context.Background()
	m := codeMessage(b)
	m.ToolName = "describeTool"
	m.Arguments = json.RawMessage(`"mcp__fixture__echo"`)
	first, err := b.Handle(ctx, "host.discover", m)
	if err != nil || first.Error != nil {
		t.Fatal(first, err)
	}
	before, _ := encoded(first)
	for _, statement := range []string{
		`UPDATE code_description_reply SET reply_json='{}'`,
		`DELETE FROM code_description_reply`,
		`INSERT OR REPLACE INTO code_description_reply SELECT execution_id,request_hash,'{}' FROM code_description_reply`,
	} {
		if _, err = s.db.Exec(statement); err == nil {
			t.Fatal("accepted description mutable", statement)
		}
	}
	h := r.pluginHost(b.t.claim)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	replayed, err := b.Handle(ctx, "host.discover", m)
	after, _ := encoded(replayed)
	if err != nil || before != after {
		t.Fatal("accepted description recomputed or changed key order", before, after, err)
	}
	// Describing accepted metadata grants no authority to dispatch an effect.
	invoke := codeMessage(b)
	invoke.Sequence = m.Sequence + 1
	denial, err := b.Handle(ctx, "host.invoke", invoke)
	if !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("cached metadata bypassed Guardian", denial, err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE parent_invocation_id IS NOT NULL AND attempt_count>0`) != 0 {
		t.Fatal("quarantined method dispatched nested work")
	}
	if _, err = s.db.Exec(`UPDATE code_execution_state SET claim_expires_at=0,version=version+1`); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	if _, err = b.Handle(ctx, "host.discover", m); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old VM read cached description", err)
	}
	// Limits were accepted in the original immutable manifest. Recovery restores
	// those values without calling the revoked policy implementation again.
	req := b.req
	req.Limits.PendingCalls = 1
	if err = b.t.prepareCodeLimits(ctx, inv, &req); err != nil || req.Limits != codemode.DefaultLimits() {
		t.Fatal("accepted envelope changed on recovery", req.Limits, err)
	}
}

func TestCodeModePluginDescriptionCommitFailureAndFrozenDenial(t *testing.T) {
	s, r, b, _ := codeClaimFixtureMode(t, true)
	ctx := context.Background()
	m := codeMessage(b)
	m.ToolName = "describeTool"
	m.Arguments = json.RawMessage(`"mcp__fixture__echo"`)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_code_description BEFORE INSERT ON code_description_reply BEGIN SELECT RAISE(ABORT,'injected cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Handle(ctx, "host.discover", m); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("cache failure acknowledged to VM", err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_description_reply`) != 0 {
		t.Fatal("uncommitted description escaped")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_code_description`); err != nil {
		t.Fatal(err)
	}
	h := r.pluginHost(b.t.claim)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	denial, err := b.Handle(ctx, "host.discover", m)
	if err != nil || denial.Error == nil || denial.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal(denial, err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_description_reply WHERE json_extract(reply_json,'$.error.code')='PROVIDER_UNAVAILABLE'`) != 1 {
		t.Fatal("denial not frozen before acknowledgment")
	}
	m.Sequence++
	next, err := b.Handle(ctx, "host.discover", m)
	if err != nil || next.Error == nil || next.Error.Code != denial.Error.Code {
		t.Fatal("frozen denial changed", next, err)
	}
}

func selectCodeModeVersion(t *testing.T, h rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	ctx := context.Background()
	pkg, err := h.Install(ctx, codeModeVersionPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "codemode", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "codemode", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "codemode-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "codemode-owner-" + version
	var limits contract.LimitsPlan
	if err = json.Unmarshal(policy.Cases[0].Expected, &limits); err != nil {
		t.Fatal(err)
	}
	if version == "1.1.0" {
		limits.Limits.PendingCalls = 64
	} else if version == "1.2.0" {
		limits.Limits.PendingCalls++
	}
	policy.Cases[0].Expected, _ = json.Marshal(limits)
	if version == "1.1.0" {
		var format contract.FormatPlan
		if err = json.Unmarshal(policy.Cases[1].Expected, &format); err != nil {
			t.Fatal(err)
		}
		format.InputType = "Readonly<" + format.InputType + ">"
		format.Guidance = "VERSION_1_1_CODEMODE"
		policy.Cases[1].Expected, _ = json.Marshal(format)
	}
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "codemode-upgrade-"+version, baseline, graph.Revision, policy.ID)
	return graph.Revision, pkg.Digest
}

func TestCodeModePluginIndependentUpgradeRetainedRunsRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, codemode.Definition())
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	r.ConfigureJavaScript(realCodeRuntime(t))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		return calls(codemode.ToolName, `{"source":"return await describeTool('mcp__fixture__echo');"}`), nil
	})
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Describe the authorized echo tool")
		if err := executeTaskRunID(t, a, r, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id string, pending int, managed bool) {
		t.Helper()
		var manifest, result string
		if err := s.db.QueryRow(`SELECT m.request_json,i.result_json FROM code_manifest m JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?`, id).Scan(&manifest, &result); err != nil {
			t.Fatal(err)
		}
		var req codemode.ExecutionRequest
		if json.Unmarshal([]byte(manifest), &req) != nil || req.Limits.PendingCalls != pending || strings.Contains(result, "VERSION_1_1_CODEMODE") != (pending == 64) {
			t.Fatal("Run changed methods or limits", manifest, result)
		}
		count := scalar(t, s, `SELECT count(*) FROM code_description_reply d JOIN code_manifest m USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?`, id)
		if (managed && count != 1) || (!managed && count != 0) {
			t.Fatal("migration boundary changed", count, managed)
		}
	}
	legacy := queue("code-native")
	h := installCodeModePlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(legacy, 128, false)
	old := queue("code-old")
	second, pkg := selectCodeModeVersion(t, h, first.Revision, "1.1.0")
	mustDrain(t, a)
	check(old, 128, true)
	current := queue("code-new")
	mustDrain(t, a)
	check(current, 64, true)
	retained := queue("code-retained")
	activateDelegation(t, h, "codemode-rollback", second, first.Revision, "codemode-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	check(retained, 64, true)
	rolled := queue("code-rolled")
	mustDrain(t, a)
	check(rolled, 128, true)
	if _, err = h.Store.Deactivate(ctx, "codemode-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := queue("code-disabled")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM code_manifest m JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?`, disabled) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND json_extract(result_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, disabled) != 1 {
		t.Fatal("disabled feature started VM or restored native policy")
	}
}

func TestCodeModePluginRealVMKeepsDescriptionsAndLimitsAcrossSuspension(t *testing.T) {
	s, r, a, p := childFixture(t, codemode.Definition())
	installCodeModePlugin(t, s, r)
	r.ConfigureJavaScript(realCodeRuntime(t))
	p.respond = delegateToolWork(func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "observed"}, nil
		}
		source := `const a=await describeTool("mcp__fixture__echo"); const b=await describeTool("mcp__fixture__echo"); const x=await tools.mcp__fixture__echo({text:"nested"}); const c=await describeTool("mcp__fixture__echo"); return {before:JSON.stringify(a),again:JSON.stringify(b),after:JSON.stringify(c),value:x};`
		raw, _ := json.Marshal(map[string]string{"source": source})
		return calls(codemode.ToolName, string(raw)), nil
	})
	mustSubmit(t, a, "managed-code-descriptions", "Execute one bounded script")
	if err := a.Drain(context.Background()); err != nil {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			t.Logf("%T: %v", cause, cause)
		}
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM code_description_reply`) != 1 || scalar(t, s, `SELECT count(*) FROM code_call`) != 1 || scalar(t, s, `SELECT count(*) FROM code_event WHERE json_extract(message_json,'$.toolName')='describeTool'`) < 3 {
		t.Fatal("actual description replay or nested effect did not run")
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
		t.Fatal("description changed object order or values during VM reconstruction", raw)
	}
	var described codemode.ToolDescription
	if json.Unmarshal([]byte(output[0].Value.Before), &described) != nil {
		t.Fatal(output)
	}
	echo, _ := a.Registry.Resolve("mcp__fixture__echo")
	if described != codemode.Describe(echo.Spec) {
		t.Fatal("managed formatter rewrote original tool fields", output[0].Value.Before, described, codemode.Describe(echo.Spec))
	}
	var manifest string
	if err := s.db.QueryRow(`SELECT request_json FROM code_manifest`).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var saved codemode.ExecutionRequest
	if json.Unmarshal([]byte(manifest), &saved) != nil || saved.Limits != codemode.DefaultLimits() || saved.SourceHash != digest(saved.Source) || !strings.Contains(saved.Source, "describeTool") {
		t.Fatal("limits or script changed during manifest acceptance", saved)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='codemode'`) != 0 {
		t.Fatal("pure methods created a separate unknown effect ledger")
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
