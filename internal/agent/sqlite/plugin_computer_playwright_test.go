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

type playwrightSurfaceProvider struct {
	surfaceProvider
	inputs      []map[string]any
	unknownFill bool
}

func (p *playwrightSurfaceProvider) Act(ctx context.Context, i computer.Instance, target computer.Target, lease, method string, args map[string]any) (any, error) {
	if computer.PlaywrightParameterMethod(method) {
		p.Mu.Lock()
		copy := map[string]any{}
		for k, v := range args {
			copy[k] = v
		}
		p.inputs = append(p.inputs, copy)
		p.Mu.Unlock()
		if p.unknownFill && method == "playwright.fill" {
			return nil, errors.Join(computer.ErrUnknown, errors.New("PRIVATE_PLAYWRIGHT_TRANSPORT"))
		}
	}
	return p.surfaceProvider.Act(ctx, i, target, lease, method, args)
}
func playwrightCapability(t *testing.T, b *codeBridge) string {
	t.Helper()
	result, err := computerPluginCall(t, b, "playwright-tab", "bind", "", "getTab", "fixture-tab")
	token := computerPluginValue(t, result, err)["handle_token"].(string)
	result, err = computerPluginCall(t, b, "playwright-list", "invoke", token, "capabilities.list")
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	result, err = computerPluginCall(t, b, "playwright-get", "invoke", token, "capabilities.get", "playwright")
	return computerPluginValue(t, result, err)["handle_token"].(string)
}
func playwrightCount(t *testing.T, b *codeBridge, cap, id string, args map[string]any) {
	t.Helper()
	result, err := computerPluginCall(t, b, id, "invoke", cap, "playwright.count", args)
	computerPluginValue(t, result, err)
}
func TestComputerPluginPlaywrightIndependentUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	p := &playwrightSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := bridges[0]
	cap := playwrightCapability(t, old)
	second, pkg := selectComputerVersion(t, h, first.Revision, "1.18.0")
	playwrightCount(t, old, cap, "old-count", map[string]any{"selector": "#field"})
	if p.inputs[0]["timeoutMs"] != 3000 {
		t.Fatal("upgrade rewrote retained default", p.inputs)
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	cap = playwrightCapability(t, next)
	playwrightCount(t, next, cap, "new-count", map[string]any{"selector": "#field"})
	playwrightCount(t, next, cap, "explicit-count", map[string]any{"selector": "#field", "timeoutMs": 7})
	if p.inputs[1]["timeoutMs"] != 4000 || p.inputs[2]["timeoutMs"] != 7 {
		t.Fatal("default or explicit value lost", p.inputs)
	}
	activateDelegation(t, h, "playwright-rollback", second, first.Revision, "computer-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	playwrightCount(t, next, cap, "retired-count", map[string]any{"selector": "#field"})
	if p.inputs[3]["timeoutMs"] != 4000 {
		t.Fatal("retired Run changed")
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	rolled := newComputerPluginExecution(t, s, r)
	cap = playwrightCapability(t, rolled)
	playwrightCount(t, rolled, cap, "rollback-count", map[string]any{"selector": "#field"})
	if p.inputs[4]["timeoutMs"] != 3000 {
		t.Fatal("rollback lost original")
	}
	if err = rolled.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "playwright-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	before := len(p.Calls)
	result, err := computerPluginCall(t, disabled, "disabled-playwright", "invoke", cap, "playwright.count", map[string]any{"selector": "#field"})
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.Calls) != before {
		t.Fatal("deactivation restored native or acquired Computer", result, err)
	}
}

func TestComputerPluginPlaywrightFrozenBeforeApprovalAndUnknownNeverReplanned(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown", false: "approved"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := selectComputerVersion(t, h, first.Revision, "1.20.0")
			p := &playwrightSurfaceProvider{unknownFill: unknown}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			cap := playwrightCapability(t, b)
			args := map[string]any{"selector": "#LIVE_NONDETERMINISTIC", "value": "original fill"}
			result, err := computerPluginCall(t, b, "frozen-fill", "invoke", cap, "playwright.fill", args)
			if !errors.Is(err, tool.ErrSuspended) || len(p.inputs) != 0 {
				t.Fatal("fill bypassed approval", result, err)
			}
			approvals, err := r.ApprovalView(ctx)
			if err != nil || len(approvals) != 1 {
				t.Fatal(approvals, err)
			}
			var approved computer.ActionParameters
			if json.Unmarshal([]byte(approvals[0].Parameters), &approved) != nil || approved.TimeoutMS == nil || approved.Selector != "#LIVE_NONDETERMINISTIC" {
				t.Fatal(approvals)
			}
			facts, _ := json.Marshal(contract.ActionFacts{Method: "playwright.fill", Surface: "tab"})
			input, _ := json.Marshal(contract.ActionArguments{Args: []any{args}})
			raw, err := h.ReadService(ctx, revision, contract.PlaywrightParametersContract, facts, []rsi.InputData{{ID: "arguments", Data: input}})
			if err != nil {
				t.Fatal(err)
			}
			var another computer.ActionParameters
			json.Unmarshal(raw, &another)
			if another.TimeoutMS == nil || *another.TimeoutMS == *approved.TimeoutMS {
				t.Fatal("fixture did not change actual default", string(raw))
			}
			approveComputerAction(t, s, r)
			result, err = computerPluginCall(t, b, "frozen-fill", "invoke", cap, "playwright.fill", args)
			if err != nil || len(p.inputs) != 1 || p.inputs[0]["timeoutMs"] != *approved.TimeoutMS || p.inputs[0]["selector"] != args["selector"] || p.inputs[0]["value"] != "original fill" {
				t.Fatal("execution changed frozen intent", result, err, p.inputs)
			}
			if unknown && (result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || strings.Contains(result.Text, "PRIVATE_")) {
				t.Fatal("unknown was hidden or leaked", result)
			}
			if !unknown && result.Error != nil {
				t.Fatal(result)
			}
			result, err = computerPluginCall(t, b, "frozen-fill", "invoke", cap, "playwright.fill", args)
			if err != nil || len(p.inputs) != 1 || scalar(t, s, `SELECT attempt_count FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND json_extract(arguments_json,'$.method')='playwright.fill'`) != 1 {
				t.Fatal("physical input replayed", result, err)
			}
			if scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
				t.Fatal("duplicate opaque plan/effect")
			}
		})
	}
}

func TestComputerPluginPlaywrightRejectsChangedSelectorAndExplicitTimeout(t *testing.T) {
	for _, v := range []string{"1.19.0", "1.21.0"} {
		t.Run(v, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, h, first.Revision, v)
			p := &playwrightSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			cap := playwrightCapability(t, b)
			before := len(p.Calls)
			result, err := computerPluginCall(t, b, "changed-playwright", "invoke", cap, "playwright.fill", map[string]any{"selector": "#LIVE_ORIGINAL", "value": "exact value", "timeoutMs": 1234})
			if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.Calls) != before || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
				t.Fatal("mechanical gate expanded original intent", result, err)
			}
		})
	}
}

func TestComputerPluginPlaywrightLateForgetGuardianAndAcceptanceFailure(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine", "write-fault"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			p := &playwrightSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := bridges[0]
			cap := playwrightCapability(t, b)
			selected, err := h.Store.Status(ctx)
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
					err = h.Store.Stop(ctx, true)
				case "quarantine":
					err = h.Store.Quarantine(ctx, selected.Revision)
				case "write-fault":
					_, err = s.db.Exec(`CREATE TRIGGER fail_playwright_plan BEFORE INSERT ON computer_action_plan BEGIN SELECT RAISE(ABORT,'injected fault');END`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before := len(p.Calls)
			args := map[string]any{"selector": "#private", "value": "PRIVATE_PLAYWRIGHT_PARAMETER"}
			result, err := computerPluginCall(t, b, "late-playwright", "invoke", cap, "playwright.fill", args)
			if !crossed || len(p.Calls) != before || result.Text != "" || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
				t.Fatal("late plan accepted", crossed, result, err)
			}
			if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(err)
			}
			if cause == "write-fault" {
				if !errors.Is(err, agent.ErrStorage) {
					t.Fatal(err)
				}
				s.MainBoundary = nil
				if _, err = s.db.Exec(`DROP TRIGGER fail_playwright_plan`); err != nil {
					t.Fatal(err)
				}
				if _, err = computerPluginCall(t, b, "late-playwright", "invoke", cap, "playwright.fill", args); !errors.Is(err, tool.ErrSuspended) {
					t.Fatal("retry needed hidden repair", err)
				}
			}
		})
	}
}

func TestComputerPluginPlaywrightPriorSevenServiceAndNativeRemainUnmanaged(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{true: "native", false: "seven-services"}[native], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if native {
					return
				}
				policy := computerOwnerPolicy(t)
				allowed := policy.AllowedContracts[:0]
				for _, c := range policy.AllowedContracts {
					if c != contract.BrowserProjectionContract && c != contract.BrowserUploadContract && c != contract.TransferPresentationContract && c != contract.PlaywrightParametersContract {
						allowed = append(allowed, c)
					}
				}
				policy.AllowedContracts = allowed
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract && c.Contract != contract.TransferPresentationContract && c.Contract != contract.PlaywrightParametersContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				h = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.3"))
			})
			if native {
				h = installComputerPlugin(t, s, r)
			}
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, h, first.Revision, "1.18.0")
			p := &playwrightSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			old := bridges[0]
			cap := playwrightCapability(t, old)
			playwrightCount(t, old, cap, "old-count", map[string]any{"selector": "#field"})
			if _, ok := p.inputs[0]["timeoutMs"]; ok || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
				t.Fatal("new service backfilled native/old Run", p.inputs)
			}
			if err = old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			cap = playwrightCapability(t, next)
			playwrightCount(t, next, cap, "new-count", map[string]any{"selector": "#field"})
			if p.inputs[1]["timeoutMs"] != 4000 {
				t.Fatal("new Run missed new method", p.inputs)
			}
		})
	}
}

func TestComputerPluginPlaywrightControlledApprovalRecoveryKeepsPlanAndCapability(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, parents := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &playwrightSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	cap := playwrightCapability(t, b)
	args := map[string]any{"selector": "#field", "value": "exact recovered fill"}
	if _, err := computerPluginCall(t, b, "recover-playwright", "invoke", cap, "playwright.fill", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	var originalHash string
	if err := s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&originalHash); err != nil {
		t.Fatal(err)
	}
	before, err := s.ComputerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wake, err := b.wake(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idle, e := b.release(ctx, wake); e != nil || !idle {
		t.Fatal(idle, e)
	}
	approveComputerAction(t, s, r)
	store := r.ToolStore(b.t.claim)
	if _, err = store.Prepare(ctx, parents[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Start(ctx, parents[0].ID); err != nil {
		t.Fatal(err)
	}
	next := *b
	next.req = b.req
	next.req.ClaimToken = ""
	next.req.ExecutionEpoch = 0
	next.attempt = newID()
	if err = next.claim(ctx, parents[0]); err != nil {
		t.Fatal(err)
	}
	// Assert the dependent invocation immediately after real recovery, without
	// extra selection, state reload or hidden fixture SQL repair.
	result, err := computerPluginCall(t, &next, "recover-playwright", "invoke", cap, "playwright.fill", args)
	if err != nil || result.Error != nil || len(p.inputs) != 1 || p.inputs[0]["selector"] != "#field" || p.inputs[0]["value"] != "exact recovered fill" || p.inputs[0]["timeoutMs"] != 3000 {
		t.Fatal("recovery lost plan or original capability", result, err, p.inputs)
	}
	after, err := s.ComputerStatus(ctx)
	if err != nil || after.Epoch != before.Epoch || after.ComputerID != before.ComputerID {
		t.Fatal("controlled approval replaced instance", before, after, err)
	}
	var hash string
	if err = s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&hash); err != nil || hash != originalHash {
		t.Fatal("recovery replaced approved plan", err)
	}
	if err = b.computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old execution owner remained valid", err)
	}
}
