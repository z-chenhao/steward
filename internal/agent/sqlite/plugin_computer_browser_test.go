package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type browserSurfaceProvider struct {
	surfaceProvider
	snapshots, legacy int
	missingSource     bool
	resources         []string
}

func browserFixtureText(text string) []byte {
	u := utf16.Encode([]rune(text))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}
func (p *browserSurfaceProvider) BrowserSnapshotResource(hash string) (computer.BrowserSnapshotter, error) {
	if hash != macosvm.BrowserSnapshotResourceHash() {
		return nil, computer.ErrCompatibility
	}
	p.resources = append(p.resources, hash)
	return p, nil
}
func (p *browserSurfaceProvider) Observe(ctx context.Context, i computer.Instance, target computer.Target, lease string, o computer.ObserveOptions) (computer.Capture, error) {
	if target.Kind == "tab" {
		p.legacy++
	}
	return p.surfaceProvider.Observe(ctx, i, target, lease, o)
}
func (p *browserSurfaceProvider) Snapshot(ctx context.Context, i computer.Instance, target computer.Target, lease string, o computer.ObserveOptions) (computer.BrowserSnapshot, error) {
	p.snapshots++
	v, err := p.Driver.Snapshot(ctx, i, target, lease, o)
	if err != nil {
		return v, err
	}
	if o.Accessibility {
		v.Source.Rows[0].Placeholder = browserFixtureText("Alternative")
		v.Source.Rows = append(v.Source.Rows, computer.BrowserSourceRow{Role: browserFixtureText("BUTTON"), Inner: browserFixtureText("Submit")})
		v.Capture.Elements = append(v.Capture.Elements, computer.Element{Index: 1, Token: "original-button", Role: "BUTTON"})
	}
	if p.missingSource {
		v.Source = computer.BrowserSource{}
	}
	return v, nil
}
func bindBrowserFixture(t *testing.T, b *codeBridge) string {
	t.Helper()
	v, e := computerPluginCall(t, b, "browser-bind", "bind", "", "getTab", "fixture-tab")
	return computerPluginValue(t, v, e)["handle_token"].(string)
}
func observeBrowserFixture(t *testing.T, b *codeBridge, token, id string) map[string]any {
	t.Helper()
	v, e := computerPluginCall(t, b, id, "invoke", token, "getAXState", map[string]any{"full": true})
	return computerPluginValue(t, v, e)
}
func browserFixtureState(t *testing.T, s *Store, b *codeBridge, token string) *computer.Handle {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT state_json FROM computer_use WHERE execution_id=?`, b.req.ExecutionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state computer.UseState
	if json.Unmarshal([]byte(raw), &state) != nil {
		t.Fatal(raw)
	}
	return state.Handles[token]
}
func TestComputerPluginBrowserProjectionIndependentUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	p := &browserSurfaceProvider{}
	p.Text = "PRIVATE_LIVE_BODY"
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	old := bridges[0]
	token := bindBrowserFixture(t, old)
	before := observeBrowserFixture(t, old, token, "before")
	if !strings.Contains(before["state"].(string), `[0] INPUT "Fixture" = "PRIVATE_LIVE_BODY"`) {
		t.Fatal(before)
	}
	original, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, pkg := selectComputerVersion(t, h, original.Revision, "1.31.0")
	if got := observeBrowserFixture(t, old, token, "retained"); got["state"] != before["state"] {
		t.Fatal("active version replaced retained plan", got)
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	upgraded := newComputerPluginExecution(t, s, r)
	newToken := bindBrowserFixture(t, upgraded)
	got := observeBrowserFixture(t, upgraded, newToken, "upgraded")
	expected := "PR\n[1] BUTTON \"Submit\"\n[0] INPUT \"Alternative\" = \"P\""
	if got["state"] != expected || p.legacy != 0 {
		t.Fatal("independent source choice not dispatched", got, p.legacy)
	}
	saved := browserFixtureState(t, s, upgraded, newToken)
	if !saved.AXReady || len(saved.Elements) != 2 || saved.Elements[0].Index != 0 || saved.Elements[0].Token != "fixture-element" || saved.Elements[1].Token != "original-button" {
		t.Fatal("presentation changed authority or reindexed native refs", saved)
	}
	activateDelegation(t, h, "browser-projection-rollback", next, original.Revision, "computer-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if got := observeBrowserFixture(t, upgraded, newToken, "retired"); got["state"] != expected {
		t.Fatal(got)
	}
	if err = upgraded.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	rolled := newComputerPluginExecution(t, s, r)
	rolledToken := bindBrowserFixture(t, rolled)
	if got := observeBrowserFixture(t, rolled, rolledToken, "rolled"); got["state"] != before["state"] {
		t.Fatal(got)
	}
	if _, err = h.Store.Deactivate(ctx, "browser-projection-disable", original.Revision); err != nil {
		t.Fatal(err)
	}
	observeBrowserFixture(t, rolled, rolledToken, "retained-after-deactivation")
	count := p.snapshots
	disabled := newComputerPluginExecution(t, s, r)
	result, err := computerPluginCall(t, disabled, "disabled", "discover", "", "getState")
	if !errors.Is(err, rsi.ErrRevoked) && (err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE") {
		t.Fatal(result, err)
	}
	if p.snapshots != count || p.legacy != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("disabled method fell back or added opaque execution ledger")
	}
}
func TestComputerPluginBrowserProjectionRejectsForgedRowsAndMissingSource(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "duplicate-rows", true: "missing-source"}[missing], func(t *testing.T) {
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			selected, err := h.Store.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !missing {
				selectComputerVersion(t, h, selected.Revision, "1.32.0")
			}
			p := &browserSurfaceProvider{missingSource: missing}
			p.Text = "PRIVATE_LIVE_BODY"
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			token := bindBrowserFixture(t, b)
			result, err := computerPluginCall(t, b, "invalid-projection", "invoke", token, "getAXState")
			if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.legacy != 0 || p.snapshots != 1 {
				t.Fatal(result, err, p.snapshots, p.legacy)
			}
			if saved := browserFixtureState(t, s, b, token); saved.AXReady || saved.AXRevision != "" {
				t.Fatal("unproved source published references", saved)
			}
		})
	}
}
func TestComputerPluginBrowserProjectionUnknownResourceRefusesBeforeEnsure(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	original.Graph.Bindings[0].Config, _ = json.Marshal(map[string]string{"browser_upload_driver": macosvm.BrowserUploadResourceHash(), "browser_snapshot_driver": strings.Repeat("0", 64)})
	invalid, err := h.Prepare(ctx, original.Graph)
	if err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "unknown-browser-resource", first.Revision, invalid.Revision, "computer-owner-fixture")
	p := &browserSurfaceProvider{}
	p.Text = "PRIVATE_LIVE_BODY"
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := newComputerPluginExecution(t, s, r)
	result, err := computerPluginCall(t, b, "unknown-resource", "discover", "", "getState")
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.ensures != 0 || p.snapshots != 0 || p.legacy != 0 {
		t.Fatal("config authorized an unowned resource", result, err, p.ensures)
	}
	old := bridges[0]
	token := bindBrowserFixture(t, old)
	observeBrowserFixture(t, old, token, "retained-resource")
	if p.snapshots != 1 || p.legacy != 0 {
		t.Fatal("retained graph followed active config")
	}
}
func TestComputerPluginBrowserProjectionLateForgetGuardianAndAcceptanceFailure(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine", "write-fault"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			p := &browserSurfaceProvider{}
			p.Text = "PRIVATE_LIVE_BODY"
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := bridges[0]
			token := bindBrowserFixture(t, b)
			selected, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := idAgent(t, s)
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "before_computer_observation_commit" {
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
					_, err = s.db.Exec(`CREATE TRIGGER fail_browser_observation BEFORE UPDATE ON computer_use BEGIN SELECT RAISE(ABORT,'injected fault');END`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := computerPluginCall(t, b, "late-browser-view", "invoke", token, "getAXStateAndScreenshot")
			want := rsi.ErrRevoked
			if cause == "forget" {
				want = agent.ErrClaimLost
			}
			if cause == "write-fault" {
				want = agent.ErrStorage
			}
			if !crossed || !errors.Is(err, want) || result.Text != "" || p.legacy != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND status='succeeded'`) != 0 {
				t.Fatal("late view accepted", crossed, result.Text, err)
			}
			if state := browserFixtureState(t, s, b, token); state.AXReady || state.ScreenshotReady {
				t.Fatal("failed view published references", state)
			}
		})
	}
}

func TestComputerPluginBrowserProjectionPriorTenServicesAndNativeRetainBoundary(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "ten-services", true: "native"}[native], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if native {
					return
				}
				policy := computerOwnerPolicy(t)
				allowed := policy.AllowedContracts[:0]
				for _, c := range policy.AllowedContracts {
					if c != contract.BrowserProjectionContract {
						allowed = append(allowed, c)
					}
				}
				policy.AllowedContracts = allowed
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				h = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.6"), computerUploadFixtureConfig())
			})
			if native {
				h = installComputerPlugin(t, s, r)
			} else {
				baseline, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectComputerVersion(t, h, baseline.Revision, "1.0.7")
			}
			p := &browserSurfaceProvider{}
			p.Text = "PRIVATE_LIVE_BODY"
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			old := bridges[0]
			token := bindBrowserFixture(t, old)
			got := observeBrowserFixture(t, old, token, "old-view")
			if p.legacy != 1 || p.snapshots != 0 || !strings.Contains(got["state"].(string), "[0] Notes") || scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) j WHERE r.run_id=? AND j.value=?`, old.req.RunID, contract.BrowserProjectionContract) != 0 {
				t.Fatal("historical graph backfilled", got, p.legacy, p.snapshots)
			}
			if err := old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			nextToken := bindBrowserFixture(t, next)
			got = observeBrowserFixture(t, next, nextToken, "new-view")
			if p.legacy != 1 || p.snapshots != 1 || !strings.Contains(got["state"].(string), `[0] INPUT "Fixture"`) {
				t.Fatal("new Run used historical renderer", got, p.legacy, p.snapshots)
			}
		})
	}
}
func TestComputerPluginBrowserProjectionControlledRecoveryKeepsOriginalResourceAndReferences(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, parents := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	p := &browserSurfaceProvider{}
	p.Text = "PRIVATE_LIVE_BODY"
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	token := bindBrowserFixture(t, b)
	view := observeBrowserFixture(t, b, token, "original-view")
	before, err := s.ComputerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = computerPluginCall(t, b, "pending-action", "invoke", token, "typeText", nil, "approved-text"); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("action bypassed original approval", err)
	}
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Store.Graph(ctx, selected.Revision)
	if err != nil {
		t.Fatal(err)
	}
	graph.Graph.Bindings[0].Config, _ = json.Marshal(map[string]string{"browser_upload_driver": macosvm.BrowserUploadResourceHash(), "browser_snapshot_driver": strings.Repeat("0", 64)})
	active, err := h.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "browser-resource-change-during-approval", selected.Revision, active.Revision, "computer-owner-fixture")
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
	// First interaction after actual release/wake/claim uses the original token;
	// there is no hidden reselect, reload, state update or fixture repair.
	recovered := observeBrowserFixture(t, &next, token, "first-after-recovery")
	if recovered["state"] != view["state"] || p.legacy != 0 || p.snapshots != 2 {
		t.Fatal("original source/resource/reference lost", recovered, p.snapshots, p.legacy)
	}
	after, err := s.ComputerStatus(ctx)
	if err != nil || after.ComputerID != before.ComputerID || after.Epoch != before.Epoch {
		t.Fatal(before, after, err)
	}
	result, err := computerPluginCall(t, &next, "pending-action", "invoke", token, "typeText", nil, "approved-text")
	computerPluginValue(t, result, err)
	if p.Text != "PRIVATE_LIVE_BODYapproved-text" || scalar(t, s, `SELECT max(attempt_count) FROM tool_invocation WHERE tool_name='builtin__computer_invoke' AND json_extract(arguments_json,'$.method')='typeText'`) != 1 {
		t.Fatal("original approved action replayed or changed", p.Text)
	}
	if err = b.computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old claim revived", err)
	}
}

func TestComputerPluginChildUsesRetainedOriginalBrowserPackage(t *testing.T) {
	ctx := context.Background()
	defs := append(computer.Definitions("pinned-test"), codemode.Definition())
	s, r, a, p := childFixture(t, defs...)
	h := installComputerPlugin(t, s, r)
	original, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalGraph, err := h.Store.Graph(ctx, original.Revision)
	if err != nil {
		t.Fatal(err)
	}
	surface := &browserSurfaceProvider{}
	surface.Text = "PRIVATE_LIVE_BODY"
	r.ConfigureComputer(surface, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	r.ConfigureJavaScript(realCodeRuntime(t))
	switched := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main final delivery."}, nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Completed delegated Computer work."}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"ROOT_COMPUTER_PRIVATE_INSTRUCTION"}`), nil
		}
		if last.Text == "child browser prototype" {
			source := `const tab=await cua.getTab("fixture-tab");const view=await tab.getAXState({full:true});return {state:view.state};`
			args, _ := json.Marshal(map[string]string{"source": source})
			return calls(codemode.ToolName, string(args)), nil
		}
		if !switched {
			selectComputerVersion(t, h, original.Revision, "1.31.0")
			if err := h.Store.Retire(ctx, originalGraph.Graph.Bindings[0].PackageDigest); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		return calls(agent.RunToolName, `{"task":"child browser prototype"}`), nil
	}
	mustSubmit(t, a, "computer-child-prototype", "MAIN_PRIVATE_CONVERSATION")
	mustDrain(t, a)
	if !switched || surface.snapshots != 1 || surface.legacy != 0 {
		t.Fatal("Child did not invoke the managed original snapshot", switched, surface.snapshots, surface.legacy)
	}
	if scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.worker_position='child' AND c.status='completed' AND cb.graph_revision=? AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json AND json_array_length(cb.managed_services_json)=11`, original.Revision) != 1 {
		t.Fatal("actual Child lost Parent graph/services")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_retired WHERE digest=?`, originalGraph.Graph.Bindings[0].PackageDigest) != 1 {
		t.Fatal("original package was not actually retired")
	}
	var childID string
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='child'`).Scan(&childID); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	requests := append([]llm.GenerationRequest(nil), p.requests...)
	p.mu.Unlock()
	seen := 0
	for _, request := range requests {
		if request.RunID != childID {
			continue
		}
		seen++
		raw, _ := json.Marshal(request.Messages)
		if strings.Contains(string(raw), "ROOT_COMPUTER_PRIVATE_INSTRUCTION") || strings.Contains(string(raw), "MAIN_PRIVATE_CONVERSATION") {
			t.Fatal("Child inherited private Parent conversation")
		}
	}
	if seen == 0 {
		t.Fatal("actual Child model requests missing")
	}
	var raw string
	if err := s.db.QueryRow(`SELECT i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name='builtin__computer_invoke' AND json_extract(i.arguments_json,'$.method')='getAXState' AND i.status='succeeded'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var receipt tool.Result
	if json.Unmarshal([]byte(raw), &receipt) != nil {
		t.Fatal(raw)
	}
	var view map[string]any
	if json.Unmarshal([]byte(receipt.Text), &view) != nil {
		t.Fatal(receipt.Text)
	}
	expected := "PRIVATE_LIVE_BODY\n[0] INPUT \"Fixture\" = \"PRIVATE_LIVE_BODY\"\n[1] BUTTON \"Submit\""
	if view["state"] != expected {
		t.Fatal("Child adopted active replacement instead of retired original", view)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
		t.Fatal("read policy added an opaque effect ledger")
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}
