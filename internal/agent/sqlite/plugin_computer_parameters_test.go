package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type parameterSurfaceProvider struct {
	selectionSurfaceProvider
	amounts      []*float64
	uncertainKey bool
}

func (p *parameterSurfaceProvider) Act(ctx context.Context, i computer.Instance, target computer.Target, lease, method string, args map[string]any) (any, error) {
	if method == "scroll" {
		var amount *float64
		if v, ok := args["amount"].(float64); ok {
			amount = &v
		}
		p.Mu.Lock()
		p.amounts = append(p.amounts, amount)
		p.Mu.Unlock()
	}
	if method == "pressKey" && p.uncertainKey {
		return nil, errors.Join(computer.ErrUnknown, errors.New("PRIVATE_KEY_RPC_UNCERTAINTY"))
	}
	return p.selectionSurfaceProvider.Act(ctx, i, target, lease, method, args)
}
func parameterTarget(t *testing.T, b *codeBridge, token, id string) map[string]any {
	t.Helper()
	result, err := computerPluginCall(t, b, id, "invoke", token, "getAXState")
	v := computerPluginValue(t, result, err)
	return map[string]any{"observationRevision": v["revision"], "index": 0}
}
func parameterScroll(t *testing.T, b *codeBridge, id, token string, target map[string]any, amount any, extra ...any) (tool.Result, error) {
	t.Helper()
	args := append([]any{target, "down", amount}, extra...)
	return computerPluginCall(t, b, id, "invoke", token, "scroll", args...)
}
func runParameterScroll(t *testing.T, s *Store, r *Runs, b *codeBridge, token, id string, amount any) {
	t.Helper()
	target := parameterTarget(t, b, token, id+"-observe")
	if _, err := parameterScroll(t, b, id, token, target, amount); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("scroll bypassed original approval", err)
	}
	approveComputerAction(t, s, r)
	result, err := parameterScroll(t, b, id, token, target, amount)
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
}

func TestComputerPluginParametersFrozenBeforeApprovalAndNeverRecomputed(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := selectComputerVersion(t, host, selected.Revision, "1.17.0")
	p := &parameterSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := newComputerPluginExecution(t, s, r)
	token := computerPluginBind(t, b)
	target := parameterTarget(t, b, token, "freeze-observe")
	if _, err = parameterScroll(t, b, "frozen-scroll", token, target, nil, "LIVE_NONDETERMINISTIC"); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approvals, err := r.ApprovalView(ctx)
	if err != nil || len(approvals) != 1 {
		t.Fatal(approvals, err)
	}
	var plan computer.ActionParameters
	if json.Unmarshal([]byte(approvals[0].Parameters), &plan) != nil || plan.Amount == nil {
		t.Fatal("approval omitted resolved effect", approvals)
	}
	var hash string
	if err = s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&hash); err != nil || hash != digest(approvals[0].Parameters) {
		t.Fatal("approval is not the immutable domain choice", err)
	}
	// This independent actual method call deliberately produces another default.
	// Executing the original approval must still use the first completed choice.
	facts, _ := json.Marshal(contract.ActionFacts{Method: "scroll", Surface: "native"})
	input, _ := json.Marshal(contract.ActionArguments{Args: []any{target, "down", nil, "LIVE_NONDETERMINISTIC"}})
	raw, err := host.ReadService(ctx, revision, contract.ActionParametersContract, facts, []rsi.InputData{{ID: "arguments", Data: input}})
	if err != nil {
		t.Fatal(err)
	}
	var next computer.ActionParameters
	if json.Unmarshal(raw, &next) != nil || next.Amount == nil || *next.Amount == *plan.Amount {
		t.Fatal("fixture did not produce an independently changing candidate", string(raw), plan)
	}
	approveComputerAction(t, s, r)
	result, err := parameterScroll(t, b, "frozen-scroll", token, target, nil, "LIVE_NONDETERMINISTIC")
	if err != nil || result.Error != nil || len(p.amounts) != 1 || p.amounts[0] == nil || *p.amounts[0] != *plan.Amount {
		t.Fatal("approved parameters recomputed", result, err, p.amounts, plan)
	}
	if scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("preparation introduced a pending opaque effect")
	}
	for _, q := range []string{`UPDATE computer_action_plan SET plan_json='{}'`, `UPDATE computer_action_plan SET plan_hash='replacement'`, `DELETE FROM computer_action_plan`, `INSERT OR REPLACE INTO computer_action_plan SELECT * FROM computer_action_plan`} {
		if _, err = s.db.Exec(q); err == nil {
			t.Fatal("immutable action plan changed", q)
		}
	}
}

func TestComputerPluginParametersIndependentUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	old := bridges[0]
	p := &parameterSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := computerPluginBind(t, old)
	second, pkg := selectComputerVersion(t, host, first.Revision, "1.15.0")
	runParameterScroll(t, s, r, old, token, "retained-default", nil)
	if p.amounts[0] != nil {
		t.Fatal("new policy rewrote old Run")
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	nextToken := computerPluginBind(t, next)
	runParameterScroll(t, s, r, next, nextToken, "new-default", nil)
	if p.amounts[1] == nil || *p.amounts[1] != 50 {
		t.Fatal("actual Driver did not use independently changed default")
	}
	runParameterScroll(t, s, r, next, nextToken, "explicit-user", 7.0)
	if p.amounts[2] == nil || *p.amounts[2] != 7 {
		t.Fatal("candidate overrode explicit user amount")
	}
	activateDelegation(t, host, "parameters-rollback", second, first.Revision, "computer-owner-fixture")
	if err = host.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	runParameterScroll(t, s, r, next, nextToken, "retained-retired", nil)
	if p.amounts[3] == nil || *p.amounts[3] != 50 {
		t.Fatal("retirement rewrote original action method")
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	reverted := newComputerPluginExecution(t, s, r)
	revertedToken := computerPluginBind(t, reverted)
	runParameterScroll(t, s, r, reverted, revertedToken, "reverted", nil)
	if p.amounts[4] != nil {
		t.Fatal("rollback did not select original defaults")
	}
	if _, err = host.Store.Deactivate(ctx, "parameters-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	acquired := p.ensures + p.restores
	result, err := computerPluginCall(t, disabled, "disabled-parameters", "discover", "", "launchApp", map[string]any{"app": "com.apple.TextEdit"})
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.ensures+p.restores != acquired {
		t.Fatal("deactivation acquired Computer or restored native parameters", result, err)
	}
}

func TestComputerPluginParametersCannotReplaceExplicitTextBeforeApproval(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectComputerVersion(t, host, first.Revision, "1.16.0")
	p := &parameterSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := newComputerPluginExecution(t, s, r)
	token := computerPluginBind(t, b)
	before := len(p.Calls)
	result, err := computerPluginCall(t, b, "wrong-text", "invoke", token, "typeText", "LIVE_ORIGINAL")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.Calls) != before || scalar(t, s, `SELECT count(*) FROM approval`) != 0 || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
		t.Fatal("candidate substituted explicit approved input", result, err)
	}
}

func TestComputerPluginParametersLateForgetGuardianAndAcceptanceFailure(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine", "write-fault"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
			b := bridges[0]
			p := &parameterSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, b)
			selected, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "before_computer_parameters_commit" {
					return
				}
				crossed = true
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				case "stop":
					err = host.Store.Stop(ctx, true)
				case "quarantine":
					err = host.Store.Quarantine(ctx, selected.Revision)
				case "write-fault":
					_, err = s.db.Exec(`CREATE TRIGGER fail_computer_plan BEFORE INSERT ON computer_action_plan BEGIN SELECT RAISE(ABORT,'injected acceptance fault');END`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before := len(p.Calls)
			result, err := computerPluginCall(t, b, "interrupted-parameters", "invoke", token, "typeText", "private parameter payload")
			if !crossed || len(p.Calls) != before || result.Text != "" || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
				t.Fatal("late rejection exposed/approved an unaccepted plan", crossed, result, err)
			}
			if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(err)
			}
			if cause == "write-fault" {
				if !errors.Is(err, agent.ErrStorage) {
					t.Fatal(err)
				}
				s.MainBoundary = nil
				if _, err = s.db.Exec(`DROP TRIGGER fail_computer_plan`); err != nil {
					t.Fatal(err)
				}
				// Immediately retry after removing the injected storage fault; no opaque
				// pending call or extra selection/repair may be needed.
				if _, err = computerPluginCall(t, b, "interrupted-parameters", "invoke", token, "typeText", "private parameter payload"); !errors.Is(err, tool.ErrSuspended) {
					t.Fatal("complete retry required a hidden repair", err)
				}
			}
		})
	}
}

func TestComputerPluginParametersForgetErasesFrozenBodyAndPreservesAudit(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &parameterSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	token := computerPluginBind(t, b)
	if _, err := computerPluginCall(t, b, "private-parameters", "invoke", token, "typeText", "PRIVATE_FROZEN_PARAMETER"); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	owner := idAgent(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM computer_action_plan WHERE plan_json IS NULL AND plan_hash=?`, hash) != 1 {
		t.Fatal("Forget changed audit identity or retained body")
	}
	if _, err := s.db.Exec(`UPDATE computer_action_plan SET plan_json='{}'`); err == nil {
		t.Fatal("erased parameters revived")
	}
	approvals, err := r.ApprovalView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range approvals {
		if strings.Contains(a.Parameters, "PRIVATE_FROZEN_PARAMETER") {
			t.Fatal("erased parameter preview survived")
		}
	}
}

func TestComputerPluginParametersPriorSixServiceAndExplicitNativeRun(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{true: "native", false: "six-services"}[native], func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if native {
					return
				}
				policy := computerOwnerPolicy(t)
				services := policy.AllowedContracts[:0]
				for _, name := range policy.AllowedContracts {
					if name != contract.BrowserProjectionContract && name != contract.BrowserUploadContract && name != contract.TransferPresentationContract && name != contract.PlaywrightParametersContract && name != contract.ActionParametersContract {
						services = append(services, name)
					}
				}
				policy.AllowedContracts = services
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract && c.Contract != contract.TransferPresentationContract && c.Contract != contract.PlaywrightParametersContract && c.Contract != contract.ActionParametersContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				host = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.2"))
			})
			old := bridges[0]
			if native {
				host = installComputerPlugin(t, s, r)
			}
			first, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, host, first.Revision, "1.15.0")
			p := &parameterSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			token := computerPluginBind(t, old)
			runParameterScroll(t, s, r, old, token, "pre-parameters", nil)
			if p.amounts[0] != nil || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
				t.Fatal("new service rewrote pre-contract action")
			}
			if scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) j WHERE r.run_id=? AND j.value=?`, old.req.RunID, contract.ActionParametersContract) != 0 {
				t.Fatal("new action service backfilled into retained Run")
			}
			if err = old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			nextToken := computerPluginBind(t, next)
			runParameterScroll(t, s, r, next, nextToken, "post-parameters", nil)
			if p.amounts[1] == nil || *p.amounts[1] != 50 {
				t.Fatal("new Run did not choose new method")
			}
		})
	}
}

func TestV64ComputerActionPlanMigrationPreservesRunRequestsAndApprovals(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db, migrations[:64]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES('historical','agent','agent','main','queued','cap','model','{"messages":[{"text":"original request"}]}',0,1,1,1);INSERT INTO rsi_run VALUES('historical',NULL,'[]');INSERT INTO tool_invocation(invocation_id,run_id,provider_exchange_id,provider_call_id,tool_name,arguments_json,request_hash,replay_policy,idempotency_key,status,delivery_seq,created_at,updated_at,version) VALUES('action','historical','exchange','call','builtin__computer_invoke','{"method":"pressKey","args":["cmd+a"]}','original-hash','never','key','waiting',0,1,1,1);INSERT INTO approval VALUES('approval','action','high','agent','original-hash','pending',NULL,100,NULL,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var checkpoint, args, hash, services, approvalHash, status string
	var version, plans int
	if err = db.QueryRow(`SELECT r.checkpoint_json,r.version,i.arguments_json,i.request_hash,b.managed_services_json,a.frozen_arguments_hash,a.status FROM run r JOIN rsi_run b USING(run_id) JOIN tool_invocation i USING(run_id) JOIN approval a USING(invocation_id) WHERE r.run_id='historical'`).Scan(&checkpoint, &version, &args, &hash, &services, &approvalHash, &status); err != nil {
		t.Fatal(err)
	}
	if checkpoint != `{"messages":[{"text":"original request"}]}` || version != 1 || args != `{"method":"pressKey","args":["cmd+a"]}` || hash != "original-hash" || services != "[]" || approvalHash != hash || status != "pending" {
		t.Fatal("upgrade rewrote retained contract", checkpoint, args, hash, services, status)
	}
	if err = db.QueryRow(`SELECT count(*) FROM computer_action_plan`).Scan(&plans); err != nil || plans != 0 {
		t.Fatal("migration invented accepted parameters", plans, err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
}

func TestComputerPluginParametersUnknownKeyEffectNeverReplansOrResends(t *testing.T) {
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &parameterSurfaceProvider{uncertainKey: true}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	token := computerPluginBind(t, b)
	call := func() (tool.Result, error) {
		return computerPluginCall(t, b, "unknown-key", "invoke", token, "pressKey", "cmd+a")
	}
	if _, err := call(); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approveComputerAction(t, s, r)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := call()
		if err != nil || result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || result.Error.Message != "UNKNOWN_SIDE_EFFECT" {
			t.Fatal(result, err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='unknown' AND attempt_count=1 AND instr(error_json,'PRIVATE_KEY_RPC_UNCERTAINTY')=0`) != 1 {
		t.Fatal("unknown key was repeated, replanned or exposed private cause")
	}
}
func TestComputerPluginParametersDescriptorOmitsOriginalResourceReferences(t *testing.T) {
	args := []any{map[string]any{"observationRevision": "PRIVATE_REVISION", "index": 5}, "down", float64(2)}
	projected := computerParameterArguments("scroll", "native", args)
	if projected[0] != nil || args[0] == nil || projected[1] != "down" || projected[2] != float64(2) {
		t.Fatal("projection changed source or disclosed identity", projected, args)
	}
	for _, method := range []string{"setValue", "selectText", "performSecondaryAction", "click"} {
		if got := computerParameterArguments(method, "native", args); got[0] != nil {
			t.Fatal(method, got)
		}
	}
	if got := computerParameterArguments("drag", "native", []any{[]any{1, 2}, []any{3, 4}}); got[0] != nil || got[1] != nil {
		t.Fatal("coordinates disclosed", got)
	}
}
