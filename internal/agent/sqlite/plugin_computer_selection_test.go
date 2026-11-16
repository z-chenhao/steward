package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type selectionSurfaceProvider struct {
	surfaceProvider
	value     string
	plans     []computer.SelectionRecipe
	uncertain bool
}

func (p *selectionSurfaceProvider) Observe(ctx context.Context, instance computer.Instance, target computer.Target, lease string, options computer.ObserveOptions) (computer.Capture, error) {
	capture, err := p.surfaceProvider.Observe(ctx, instance, target, lease, options)
	if len(capture.Elements) > 0 {
		capture.Elements[0].Value, capture.Elements[0].Role = p.value, "AXTextField"
	}
	return capture, err
}

func (p *selectionSurfaceProvider) Act(ctx context.Context, instance computer.Instance, target computer.Target, lease, method string, args map[string]any) (any, error) {
	if method == "selectText" {
		dispatch := args["selection_dispatch"].(computer.SelectionDispatch)
		if err := dispatch.BeforeInput(ctx); err != nil {
			return nil, err
		}
		p.Mu.Lock()
		p.plans = append(p.plans, dispatch.Recipe)
		p.Mu.Unlock()
		if p.uncertain {
			return nil, errors.Join(computer.ErrUnknown, errors.New("PRIVATE_TRANSIENT_INPUT_GUARD_FAULT"))
		}
	}
	return p.surfaceProvider.Act(ctx, instance, target, lease, method, args)
}

func computerSelectionCall(t *testing.T, b *codeBridge, id, token, revision string) (tool.Result, error) {
	t.Helper()
	return computerPluginCall(t, b, id, "invoke", token, "selectText", map[string]any{"observationRevision": revision, "index": 0}, "target")
}

func approveComputerAction(t *testing.T, s *Store, r *Runs) {
	t.Helper()
	var id, hash, original, policy string
	if err := s.db.QueryRow(`SELECT a.approval_id,a.frozen_arguments_hash,i.request_hash,i.policy_json FROM approval a JOIN tool_invocation i USING(invocation_id) WHERE a.status='pending'`).Scan(&id, &hash, &original, &policy); err != nil {
		t.Fatal(err)
	}
	if hash != original {
		t.Fatal("action did not keep its exact original action hash")
	}
	var never bool
	if err := s.db.QueryRow(`SELECT json_extract(?,'$.replay')='never' AND json_extract(?,'$.risk')='high'`, policy, policy).Scan(&never); err != nil || !never {
		t.Fatal("action weakened original approval or unknown policy", policy, err)
	}
	diagnostics, err := s.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Decide(context.Background(), diagnostics.AgentID, id, hash, "approved"); err != nil {
		t.Fatal(err)
	}
}

func runComputerSelection(t *testing.T, s *Store, r *Runs, b *codeBridge, token, id string) tool.Result {
	t.Helper()
	observed, err := computerPluginCall(t, b, id+"-observation", "invoke", token, "getAXState")
	view := computerPluginValue(t, observed, err)
	if _, err = computerSelectionCall(t, b, id, token, view["revision"].(string)); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("selection bypassed exact approval", err)
	}
	approveComputerAction(t, s, r)
	result, err := computerSelectionCall(t, b, id, token, view["revision"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestComputerPluginSelectionIndependentUpgradeRollbackRetainedAndDisabled(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	b := bridges[0]
	provider := &selectionSurfaceProvider{value: "one target end"}
	r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := computerPluginBind(t, b)
	second, pkg := selectComputerVersion(t, host, first.Revision, "1.9.0")
	if result := runComputerSelection(t, s, r, b, token, "retained-v1"); result.Error != nil || provider.plans[0].Anchor != "start" {
		t.Fatal("new version rewrote old Run selection", result, provider.plans)
	}
	if err = b.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	nextToken := computerPluginBind(t, next)
	if result := runComputerSelection(t, s, r, next, nextToken, "new-end-anchor"); result.Error != nil || provider.plans[1] != (computer.SelectionRecipe{Anchor: "end", Move: -4, Extend: -6}) {
		t.Fatal("independent selection algorithm did not reach effect adapter", result, provider.plans)
	}
	activateDelegation(t, host, "selection-rollback", second, first.Revision, "computer-owner-fixture")
	if err = host.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if result := runComputerSelection(t, s, r, next, nextToken, "retired-end-anchor"); result.Error != nil || provider.plans[2].Anchor != "end" {
		t.Fatal("retirement or rollback rewrote retained selection", result, provider.plans)
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	reverted := newComputerPluginExecution(t, s, r)
	revertedToken := computerPluginBind(t, reverted)
	if result := runComputerSelection(t, s, r, reverted, revertedToken, "reverted-start-anchor"); result.Error != nil || provider.plans[3].Anchor != "start" {
		t.Fatal("new Run did not take rolled-back method", result, provider.plans)
	}
	if _, err = host.Store.Deactivate(ctx, "selection-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	before := len(provider.Calls)
	acquired := provider.ensures + provider.restores
	result, err := computerPluginCall(t, disabled, "disabled-selection", "bind", "", "getApp", "notes")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(provider.Calls) != before || provider.ensures+provider.restores != acquired || len(provider.plans) != 4 {
		t.Fatal("deactivation revived native policy or acquired a Computer", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("pure action plan created another opaque effect ledger")
	}
}

func TestComputerPluginSelectionMechanicalGateCannotAuthorizeWrongSpan(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectComputerVersion(t, host, first.Revision, "1.10.0")
	provider := &selectionSurfaceProvider{value: "one target end"}
	r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := newComputerPluginExecution(t, s, r)
	token := computerPluginBind(t, b)
	result := runComputerSelection(t, s, r, b, token, "wrong-span")
	if result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(provider.plans) != 0 {
		t.Fatal("mechanical candidate evidence replaced exact approved source proof", result, provider.plans)
	}
}

func TestComputerPluginSelectionUnknownInputNeverReplays(t *testing.T) {
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	provider := &selectionSurfaceProvider{value: "one target end", uncertain: true}
	r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	token := computerPluginBind(t, b)
	observed, err := computerPluginCall(t, b, "uncertain-selection-observe", "invoke", token, "getAXState")
	view := computerPluginValue(t, observed, err)
	revision := view["revision"].(string)
	if _, err = computerSelectionCall(t, b, "uncertain-selection", token, revision); !errors.Is(err, tool.ErrSuspended) || len(provider.plans) != 0 {
		t.Fatal("selection was dispatched without approval", err, provider.plans)
	}
	approveComputerAction(t, s, r)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := computerSelectionCall(t, b, "uncertain-selection", token, revision)
		if err != nil || result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || result.Error.Message != "UNKNOWN_SIDE_EFFECT" || len(provider.plans) != 1 {
			encodedResult, _ := encoded(result)
			t.Fatal("unknown keyboard effect was misclassified or repeated", attempt, encodedResult, err, provider.plans)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND json_extract(arguments_json,'$.method')='selectText' AND status='unknown' AND attempt_count=1 AND json_extract(error_json,'$.code')='UNKNOWN_SIDE_EFFECT' AND instr(error_json,'PRIVATE_TRANSIENT_INPUT_GUARD_FAULT')=0`) != 1 {
		t.Fatal("unknown native effect lost its durable state or exposed private cause")
	}
}

func TestComputerPluginSelectionKeepsExplicitNativeRunAfterActivation(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t)
	b := bridges[0]
	host := installComputerPlugin(t, s, r)
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectComputerVersion(t, host, first.Revision, "1.9.0")
	provider := &selectionSurfaceProvider{value: "one target end"}
	r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	token := computerPluginBind(t, b)
	if result := runComputerSelection(t, s, r, b, token, "native-original-selection"); result.Error != nil || len(provider.plans) != 1 || provider.plans[0].Anchor != "start" {
		t.Fatal("later plugin activation changed the retained native execution", result, provider.plans)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("explicit native boundary became a plugin invocation")
	}
}

func TestComputerPluginSelectionAdditionPreservesPriorThreeServicePackageAndRun(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
		policy := computerOwnerPolicy(t)
		services := policy.AllowedContracts[:0]
		for _, service := range policy.AllowedContracts {
			if service != contract.BrowserProjectionContract && service != contract.BrowserUploadContract && service != contract.TransferPresentationContract && service != contract.PlaywrightParametersContract && service != contract.SelectionContract && service != contract.InventoryOrderContract && service != contract.TargetMatchContract && service != contract.ActionParametersContract {
				services = append(services, service)
			}
		}
		policy.AllowedContracts = services
		cases := policy.Cases[:0]
		for _, c := range policy.Cases {
			if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract && c.Contract != contract.TransferPresentationContract && c.Contract != contract.PlaywrightParametersContract && c.Contract != contract.SelectionContract && c.Contract != contract.InventoryOrderContract && c.Contract != contract.TargetMatchContract && c.Contract != contract.ActionParametersContract {
				cases = append(cases, c)
			}
		}
		policy.Cases = cases
		host = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.0"))
	})
	old := bridges[0]
	provider := &selectionSurfaceProvider{value: "one target end"}
	r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectComputerVersion(t, host, first.Revision, "1.9.0")
	if scalar(t, s, `SELECT count(*) FROM rsi_run WHERE run_id=? AND graph_revision=? AND json_array_length(managed_services_json)=3 AND NOT EXISTS(SELECT 1 FROM json_each(managed_services_json) WHERE value=?)`, old.req.RunID, first.Revision, contract.SelectionContract) != 1 {
		t.Fatal("additive service rewrote original package/Run boundary")
	}
	token := computerPluginBind(t, old)
	if result := runComputerSelection(t, s, r, old, token, "old-three-service-selection"); result.Error != nil || len(provider.plans) != 1 || provider.plans[0].Anchor != "start" {
		t.Fatal("retained pre-selection Run lost its original algorithm", result, provider.plans)
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	nextToken := computerPluginBind(t, next)
	if result := runComputerSelection(t, s, r, next, nextToken, "new-four-service-selection"); result.Error != nil || len(provider.plans) != 2 || provider.plans[1].Anchor != "end" {
		t.Fatal("new service did not use independently upgraded algorithm", result, provider.plans)
	}
}

func TestComputerPluginSelectionLateForgetAndGuardianRejectInput(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
			provider := &selectionSurfaceProvider{value: "one target end"}
			r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			selected, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "before_computer_selection_dispatch" {
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
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			b := bridges[0]
			token := computerPluginBind(t, b)
			observed, err := computerPluginCall(t, b, "guarded-selection-observe", "invoke", token, "getAXState")
			view := computerPluginValue(t, observed, err)
			if _, err = computerSelectionCall(t, b, "guarded-selection", token, view["revision"].(string)); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			approveComputerAction(t, s, r)
			result, err := computerSelectionCall(t, b, "guarded-selection", token, view["revision"].(string))
			if !crossed || len(provider.plans) != 0 {
				t.Fatal("late authority loss sent native input", result, err)
			}
			if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal("Forget cause lost at input boundary", result, err)
			}
		})
	}
}
