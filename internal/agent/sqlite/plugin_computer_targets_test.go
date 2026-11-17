package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type targetSurfaceProvider struct {
	surfaceProvider
	values      map[string][]computer.Target
	inventories []string
	observed    []computer.Target
}

func (p *targetSurfaceProvider) Inventory(_ context.Context, _ computer.Instance, kind string) ([]computer.Target, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.inventories = append(p.inventories, kind)
	return append([]computer.Target(nil), p.values[kind]...), nil
}
func (p *targetSurfaceProvider) Observe(ctx context.Context, i computer.Instance, target computer.Target, lease string, o computer.ObserveOptions) (computer.Capture, error) {
	p.Mu.Lock()
	p.observed = append(p.observed, target)
	p.Mu.Unlock()
	return p.surfaceProvider.Observe(ctx, i, target, lease, o)
}
func targetProvider() *targetSurfaceProvider {
	return &targetSurfaceProvider{values: map[string][]computer.Target{
		"apps":     {{ID: "z", Kind: "app", Name: "Zulu", Native: map[string]any{"pid": "PRIVATE_NATIVE_ID"}}, {ID: "a", Kind: "app", Name: "Alpha"}},
		"browsers": {{ID: "b", Kind: "browser", Name: "Browser"}},
		"tabs":     {{ID: "t", Kind: "tab", Name: "Tab", Browser: "b", URL: "https://example.test"}, {ID: "other", Kind: "tab", Name: "Other", Browser: "other-browser"}},
		"windows":  {{ID: "window", Kind: "window", Name: "Window"}},
	}}
}
func targetBind(t *testing.T, b *codeBridge, id, selector string) map[string]any {
	t.Helper()
	result, err := computerPluginCall(t, b, id, "bind", "", "getApp", selector)
	return computerPluginValue(t, result, err)
}
func targetOrder(t *testing.T, b *codeBridge, id string) []string {
	t.Helper()
	result, err := computerPluginCall(t, b, id, "discover", "", "listApps")
	if err != nil || result.Error != nil {
		t.Fatal(result, err)
	}
	var values []map[string]any
	if err = json.Unmarshal([]byte(result.Text), &values); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, v := range values {
		ids = append(ids, v["id"].(string))
		if _, ok := v["native"]; ok {
			t.Fatal("native identity disclosed")
		}
	}
	return ids
}
func TestComputerPluginTargetsIndependentUpgradeRollbackAndFrozenBinding(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	p := targetProvider()
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	old := bridges[0]
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	binding := targetBind(t, old, "old-exact", "z")
	token := binding["handle_token"].(string)
	if got := targetOrder(t, old, "old-order"); fmt.Sprint(got) != "[z a]" {
		t.Fatal(got)
	}
	second, pkg := selectComputerVersion(t, host, first.Revision, "1.11.0")
	if got := targetOrder(t, old, "retained-order"); fmt.Sprint(got) != "[z a]" {
		t.Fatal("old Run changed", got)
	}
	result, err := computerPluginCall(t, old, "retained-exact-match", "bind", "", "getApp", "alpha")
	if err != nil || result.Error == nil || result.Error.Code != "NOT_FOUND" {
		t.Fatal("old Run got new name policy", result, err)
	}
	// A later inventory now associates the old label with another native object.
	// An already bound handle must still observe the original target.
	p.values["apps"] = []computer.Target{{ID: "replacement", Kind: "app", Name: "Zulu"}}
	result, err = computerPluginCall(t, old, "frozen-target", "invoke", token, "getAXState")
	computerPluginValue(t, result, err)
	if len(p.observed) != 1 || p.observed[0].ID != "z" || p.observed[0].Native["pid"] != "PRIVATE_NATIVE_ID" {
		t.Fatal("binding re-resolved after approval boundary", p.observed)
	}
	p.values = targetProvider().values
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	if got := targetOrder(t, next, "new-order"); fmt.Sprint(got) != "[a z]" {
		t.Fatal("new sorting implementation unused", got)
	}
	if got := targetBind(t, next, "new-casefold", "alpha")["ref"].(map[string]any)["id"]; got != "a" {
		t.Fatal(got)
	}
	activateDelegation(t, host, "targets-rollback", second, first.Revision, "computer-owner-fixture")
	if err = host.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if got := targetOrder(t, next, "retained-retired-order"); fmt.Sprint(got) != "[a z]" {
		t.Fatal(got)
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	reverted := newComputerPluginExecution(t, s, r)
	if got := targetOrder(t, reverted, "reverted-order"); fmt.Sprint(got) != "[z a]" {
		t.Fatal(got)
	}
	if _, err = host.Store.Deactivate(ctx, "targets-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	before := len(p.inventories)
	acquired := p.ensures + p.restores
	result, err = computerPluginCall(t, disabled, "disabled-targets", "bind", "", "getApp", "z")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.inventories) != before || p.ensures+p.restores != acquired {
		t.Fatal("deactivation revived native path", result, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("pure targets policy created opaque effects")
	}
}

func TestComputerPluginTargetsBatchCompletenessScopeAndAmbiguity(t *testing.T) {
	_, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := targetProvider()
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	// Inventory exceeds the plugin's 8 MiB input ceiling, while the original
	// selected result remains small. Match the final record after several pages.
	p.values["apps"] = nil
	for i := 0; i < 1100; i++ {
		p.values["apps"] = append(p.values["apps"], computer.Target{ID: fmt.Sprintf("i%d", i), Kind: "app", Name: strings.Repeat("无", 3000)})
	}
	p.values["apps"] = append(p.values["apps"], computer.Target{ID: "last", Kind: "app", Name: "Last"})
	if got := targetBind(t, b, "last-page", "Last")["ref"].(map[string]any)["id"]; got != "last" {
		t.Fatal(got)
	}
	if len(p.inventories) != 1 {
		t.Fatal("batching re-read a changing Driver inventory", p.inventories)
	}
	p.values["apps"][0].Name = "Last"
	result, err := computerPluginCall(t, b, "cross-page-ambiguity", "bind", "", "getApp", "Last")
	if err != nil || result.Error == nil || result.Error.Code != "NOT_FOUND" {
		t.Fatal("ambiguous cross-page label selected arbitrary target", result, err)
	}
	p.values["apps"] = []computer.Target{{ID: "id", Kind: "app", Name: "Only"}, {ID: "other", Kind: "app", Name: "id"}}
	result, err = computerPluginCall(t, b, "id-name-ambiguity", "bind", "", "getApp", "id")
	if err != nil || result.Error == nil || result.Error.Code != "NOT_FOUND" {
		t.Fatal("original ID/name ambiguity changed", result, err)
	}
	longSelector := strings.Repeat("x", 60000)
	p.values["apps"] = []computer.Target{{ID: "long", Kind: "app", Name: longSelector}}
	if got := targetBind(t, b, "long-selector", longSelector)["ref"].(map[string]any)["id"]; got != "long" {
		t.Fatal("new matcher narrowed original selector size", got)
	}
	p.values["apps"] = targetProvider().values["apps"]
	result, err = computerPluginCall(t, b, "window-explicit", "bind", "", "getApp", map[string]any{"windowId": "window", "id": "z"})
	if got := computerPluginValue(t, result, err)["ref"].(map[string]any)["id"]; got != "window" {
		t.Fatal("window scope lost", got)
	}
	result, err = computerPluginCall(t, b, "browser-scope", "discover", "", "listTabs", map[string]any{"browser": "b"})
	if err != nil || result.Error != nil || !strings.Contains(result.Text, `"id":"t"`) || strings.Contains(result.Text, `"id":"other"`) {
		t.Fatal("browser scope widened", result, err)
	}
}

func TestComputerPluginTargetsRejectInvalidCandidateDespiteOwnerGate(t *testing.T) {
	for _, version := range []string{"1.12.0", "1.13.0", "1.14.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
			first, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, host, first.Revision, version)
			p := targetProvider()
			p.values["apps"][0].ID = "LIVE_PIN"
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			operation, method := "bind", "getApp"
			args := []any{"LIVE_PIN"}
			if version == "1.12.0" {
				operation, method, args = "discover", "listApps", nil
			}
			result, err := computerPluginCall(t, b, "bad-targets", operation, "", method, args...)
			if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || result.Text != "" {
				t.Fatal("candidate corrupted source set or exact identity", result, err)
			}
			if scalar(t, s, `SELECT count(*) FROM computer_use u,json_each(u.state_json,'$.handles')`) != 0 {
				t.Fatal("invalid selection minted a handle")
			}
		})
	}
}

func TestComputerPluginTargetsLateForgetGuardianAndEpochRejectCommit(t *testing.T) {
	for _, operation := range []string{"bind", "discover"} {
		for _, cause := range []string{"forget", "stop", "quarantine", "idle"} {
			t.Run(operation+"/"+cause, func(t *testing.T) {
				ctx := context.Background()
				var host rsi.Host
				s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
				b := bridges[0]
				r.ConfigureComputer(targetProvider(), computer.Compatibility{}, computer.RestorationManifest{Version: 1})
				selected, err := host.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				owner := idAgent(t, s)
				crossed := false
				point := "before_computer_binding_commit"
				method := "getApp"
				args := []any{"z"}
				if operation == "discover" {
					point = "before_computer_inventory_commit"
					method = "listApps"
					args = nil
				}
				s.MainBoundary = func(p string) {
					if p != point {
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
					case "idle":
						err = b.computer().Idle(ctx)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				result, err := computerPluginCall(t, b, "revoked-target", operation, "", method, args...)
				want := rsi.ErrRevoked
				if cause == "forget" || cause == "idle" {
					want = agent.ErrClaimLost
				}
				if !crossed || !errors.Is(err, want) || result.Text != "" {
					t.Fatal("late revocation exposed target", crossed, result, err)
				}
				if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded'`, "builtin__computer_"+operation) != 0 {
					t.Fatal("revoked target receipt accepted")
				}
			})
		}
	}
}

func TestComputerPluginTargetsPreservePriorFourServicePackageAndNativeRuns(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			ctx := context.Background()
			var host rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if legacy {
					return
				}
				policy := computerOwnerPolicy(t)
				allowed := policy.AllowedContracts[:0]
				for _, name := range policy.AllowedContracts {
					if name != contract.BrowserProjectionContract && name != contract.BrowserUploadContract && name != contract.TransferPresentationContract && name != contract.PlaywrightParametersContract && name != contract.InventoryOrderContract && name != contract.TargetMatchContract && name != contract.ActionParametersContract {
						allowed = append(allowed, name)
					}
				}
				policy.AllowedContracts = allowed
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract && c.Contract != contract.TransferPresentationContract && c.Contract != contract.PlaywrightParametersContract && c.Contract != contract.InventoryOrderContract && c.Contract != contract.TargetMatchContract && c.Contract != contract.ActionParametersContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				host = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.1"))
			})
			old := bridges[0]
			if legacy {
				host = installComputerPlugin(t, s, r)
			}
			first, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, host, first.Revision, "1.11.0")
			p := targetProvider()
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			if got := targetOrder(t, old, "old-boundary-order"); fmt.Sprint(got) != "[z a]" {
				t.Fatal(got)
			}
			result, err := computerPluginCall(t, old, "old-boundary-name", "bind", "", "getApp", "alpha")
			if err != nil || result.Error == nil || result.Error.Code != "NOT_FOUND" {
				t.Fatal("new algorithm rewrote pre-contract Run", result, err)
			}
			if scalar(t, s, `SELECT count(*) FROM rsi_run b,json_each(b.managed_services_json) j WHERE b.run_id=? AND j.value IN (?,?)`, old.req.RunID, contract.InventoryOrderContract, contract.TargetMatchContract) != 0 {
				t.Fatal("new services backfilled into old Run")
			}
			if err = old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			if got := targetOrder(t, next, "new-boundary-order"); fmt.Sprint(got) != "[a z]" {
				t.Fatal(got)
			}
			targetBind(t, next, "new-boundary-name", "alpha")
		})
	}
}

func TestComputerMatchTextPreservesCompleteUnicodeMatches(t *testing.T) {
	for _, s := range []string{"Notes", "Kelvin", "世界", strings.Repeat("x", 65536)} {
		got := computerMatchText(s, max(256, 4*len(s)))
		if !got.Complete || got.Text != s {
			t.Fatal("legitimate match truncated")
		}
	}
	value := strings.Repeat("世", 1000)
	got := computerMatchText(value, 256)
	if got.Complete || len(got.Text) > 256 || !utf8.ValidString(got.Text) || !strings.HasPrefix(value, got.Text) {
		t.Fatal(got)
	}
}

func TestComputerPluginTargetsCreateLateGuardianNeverReplaysEffect(t *testing.T) {
	ctx := context.Background()
	var host rsi.Host
	s, r, bridges, parents := computerPluginClaims(t, func(s *Store, r *Runs) { host = installComputerPlugin(t, s, r) })
	p := targetProvider()
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	call := func() (tool.Result, error) {
		return computerPluginCall(t, b, "create-late-guardian", "bind", "", "createBrowserTab", "b", "https://example.test")
	}
	if _, err := call(); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("create bypassed original approval", err)
	}
	approveComputerAction(t, s, r)
	crossed := false
	s.MainBoundary = func(point string) {
		if point != "before_computer_binding_commit" {
			return
		}
		crossed = true
		if err := host.Store.Stop(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	result, err := call()
	if !crossed || !errors.Is(err, rsi.ErrRevoked) || result.Text != "" {
		t.Fatal("late guardian accepted a created handle", result, err)
	}
	// Remove the injected Guardian fault, then immediately retry the original
	// invocation. The real ToolExecutor must reconcile uncertainty, not create.
	s.MainBoundary = nil
	if err = host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	// Reclaim the original Run and Code execution through production APIs.
	// Same-owner retries are deliberately busy while an attempt is running.
	if err = r.Release(ctx, b.t.claim); err != nil {
		t.Fatal(err)
	}
	recoveredClaim, err := r.Claim(ctx, b.req.RunID)
	if err != nil {
		t.Fatal(err)
	}
	next := &codeBridge{t: r.ToolStore(recoveredClaim).(*tools), registry: b.registry, attempt: newID(), req: b.req}
	next.req.ClaimToken = ""
	next.req.ExecutionEpoch = 0
	if err = next.claim(ctx, parents[0]); err != nil {
		t.Fatal(err)
	}
	b = next
	result, err = call()
	count := 0
	for _, method := range p.Calls {
		if method == "createBrowserTab" {
			count++
		}
	}
	if err != nil || result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || count != 1 {
		t.Fatal("unaccepted create effect repeated", result, err, p.Calls)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_bind' AND status='unknown' AND attempt_count=1`) != 1 {
		t.Fatal("create uncertainty not durable")
	}
}
