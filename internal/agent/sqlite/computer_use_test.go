package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	computerfake "github.com/z-chenhao/assistant/internal/computer/fake"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

type surfaceProvider struct {
	lifecycleFake
	computerfake.Driver
	unknown bool
}

func (p *surfaceProvider) Act(ctx context.Context, i computer.Instance, target computer.Target, lease, method string, args map[string]any) (any, error) {
	v, e := p.Driver.Act(ctx, i, target, lease, method, args)
	if p.unknown && method == "typeText" {
		return nil, computer.ErrUnknown
	}
	return v, e
}
func TestComputerApprovalControlledLeaseResumeAndUnknownNeverDispatchesAgain(t *testing.T) {
	testComputerApprovalControlledLeaseResumeAndUnknownNeverDispatchesAgain(t, false)
}
func TestComputerPluginApprovalControlledLeaseResumeAndUnknownNeverDispatchesAgain(t *testing.T) {
	testComputerApprovalControlledLeaseResumeAndUnknownNeverDispatchesAgain(t, true)
}
func testComputerApprovalControlledLeaseResumeAndUnknownNeverDispatchesAgain(t *testing.T, managed bool) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "approved", true: "unknown"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			var configure []func(*Store, *Runs)
			if managed {
				configure = append(configure, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
			}
			s, r, bridges, parents := computerClaims(t, configure...)
			b := bridges[0]
			provider := &surfaceProvider{unknown: unknown}
			r.ConfigureComputer(provider, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			executor := func(bridge *codeBridge) tool.Executor {
				return tool.Executor{Registry: bridge.registry, Store: nestedTools{bridge.t.forCode(bridge), bridge.req.ExecutionID}}
			}
			bind := []llm.ToolCall{{ID: b.req.ExecutionID + "/" + digest("bind"), Name: "builtin__computer_bind", Arguments: `{"method":"getApp","args":["notes"]}`}}
			result, err := executor(b).Execute(ctx, b.exchange, bind)
			if err != nil || result[0].Error != nil {
				t.Fatal(result, err)
			}
			var handle struct {
				Token string `json:"handle_token"`
			}
			json.Unmarshal([]byte(result[0].Text), &handle)
			raw, _ := encoded(map[string]any{"method": "typeText", "handle_token": handle.Token, "args": []any{"approved fixture"}})
			action := []llm.ToolCall{{ID: b.req.ExecutionID + "/" + digest("action"), Name: "builtin__computer_invoke", Arguments: raw}}
			if _, err = executor(b).Execute(ctx, b.exchange, action); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			if provider.Text != "" {
				t.Fatal("unapproved dispatch")
			}
			before, _ := s.ComputerStatus(ctx)
			wake, _ := b.wake(ctx)
			if idle, e := b.release(ctx, wake); e != nil || !idle {
				t.Fatal(idle, e)
			}
			approvePolicy(t, s, r)
			rootStore := b.t.runs.ToolStore(b.t.claim)
			if _, err = rootStore.Prepare(ctx, parents[0].ID); err != nil {
				t.Fatal(err)
			}
			if err = rootStore.Start(ctx, parents[0].ID); err != nil {
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
			if err = b.computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal("old lease owner survived", err)
			}
			result, err = executor(&next).Execute(ctx, next.exchange, action)
			if err != nil {
				t.Fatal(err)
			}
			if unknown {
				if result[0].Error == nil || result[0].Error.Code != "UNKNOWN_SIDE_EFFECT" {
					t.Fatal(result)
				}
			} else if result[0].Error != nil {
				t.Fatal(result)
			}
			after, _ := s.ComputerStatus(ctx)
			if before.Epoch != after.Epoch || before.ComputerID != after.ComputerID || provider.Text != "approved fixture" {
				t.Fatal("controlled approval rebuilt instance or lost handle", before, after, provider.Text)
			}
			result, err = executor(&next).Execute(ctx, next.exchange, action)
			if err != nil || provider.Text != "approved fixture" {
				t.Fatal("replayed mutation", result, err, provider.Text)
			}
			if scalar(t, s, `SELECT attempt_count FROM tool_invocation WHERE tool_name='builtin__computer_invoke'`) != 1 {
				t.Fatal("effect was retried")
			}
		})
	}
}
func TestComputerDesktopBarrierAppliesAcrossAppAndRead(t *testing.T) {
	ctx := context.Background()
	s, r, b, _ := computerClaims(t)
	p := &surfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	nested := nestedTools{b[0].t.forCode(b[0]), b[0].req.ExecutionID}
	calls := []llm.ToolCall{{ID: b[0].req.ExecutionID + "/" + digest("one"), Name: "builtin__computer_discover", Arguments: `{"method":"getState"}`}, {ID: b[0].req.ExecutionID + "/" + digest("two"), Name: "builtin__computer_discover", Arguments: `{"method":"listApps"}`}}
	invs, err := nested.Accept(ctx, b[0].exchange, calls, b[0].registry.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err = nested.Start(ctx, invs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = nested.Start(ctx, invs[1].ID); !errors.Is(err, tool.ErrResource) {
		t.Fatal("observation bypassed desktop barrier", err)
	}
	if err = nested.Finish(ctx, invs[0].ID, tool.Result{Text: `{}`}); err != nil {
		t.Fatal(err)
	}
	if err = nested.Start(ctx, invs[1].ID); err != nil {
		t.Fatal("barrier did not release", err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name LIKE 'builtin__computer_%' AND json_array_length(conflict_keys)>0`) != 2 {
		t.Fatal("conflict keys not frozen")
	}
}

func TestComputerUseStateCASAndV21Upgrade(t *testing.T) {
	s, r, bridges, _ := computerClaims(t)
	ctx := context.Background()
	r.ConfigureComputer(&lifecycleFake{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	if _, err := bridges[0].computer().Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	a := &computerUseStore{b: bridges[0]}
	b := &computerUseStore{b: bridges[0]}
	x, err := a.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	y, err := b.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	x.Files = map[string]bool{"imports/a": true}
	y.Files = map[string]bool{"imports/b": true}
	if err = a.Save(ctx, x); err != nil {
		t.Fatal(err)
	}
	if err = b.Save(ctx, y); !errors.Is(err, computer.ErrStale) {
		t.Fatal("lost update", err)
	}
	other := &computerUseStore{b: bridges[1]}
	visible, err := other.Load(ctx)
	if err != nil || !visible.Files["imports/a"] {
		t.Fatal("scoped registration lost across cells", visible, err)
	}
	if _, err = s.db.Exec(`UPDATE computer_use SET version=version+2`); err == nil {
		t.Fatal("illegal version")
	}
	var frozen string
	if err = s.db.QueryRow(`SELECT request_json FROM model_generation LIMIT 1`).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(removeAutomationFixtureSchema + `DROP TABLE computer_file; DROP TABLE computer_use; DROP TABLE computer_lease_resume; PRAGMA user_version=21`); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(s.path)
	s.Close()
	next, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	var after string
	if err = next.db.QueryRow(`SELECT request_json FROM model_generation LIMIT 1`).Scan(&after); err != nil || after != frozen {
		t.Fatal("upgrade changed accepted request", err)
	}
	if scalar(t, next, `PRAGMA user_version`) != len(migrations) || scalar(t, next, `SELECT count(*) FROM computer_use`) != 0 {
		t.Fatal("v21 upgrade failed")
	}
}

func TestComputerScreenshotNormalizationCommitsArtifactAndReadinessTogether(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerClaims(t)
	b := bridges[0]
	r.ConfigureComputer(&surfaceProvider{}, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	executor := tool.Executor{Registry: b.registry, Store: nestedTools{b.t.forCode(b), b.req.ExecutionID}}
	run := func(key, method, token string) tool.Result {
		args, _ := encoded(map[string]any{"method": method, "handle_token": token, "args": []any{"notes"}})
		name := "builtin__computer_invoke"
		if method == "getApp" {
			name = "builtin__computer_bind"
		}
		out, err := executor.Execute(ctx, b.exchange, []llm.ToolCall{{ID: b.req.ExecutionID + "/" + digest(key), Name: name, Arguments: args}})
		if err != nil || len(out) != 1 {
			t.Fatal(out, err)
		}
		return out[0]
	}
	bound := run("bind", "getApp", "")
	var handle struct {
		Token string `json:"handle_token"`
	}
	if err := json.Unmarshal([]byte(bound.Text), &handle); err != nil {
		t.Fatal(err)
	}
	s.Artifacts().Policy.MaxBytes = 1
	failed := run("quota", "getAXStateAndScreenshot", handle.Token)
	if failed.Error == nil || failed.Error.Code != "RESOURCE_LIMIT" {
		t.Fatal(failed)
	}
	state, err := (&computerUseStore{b: b}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Handles[handle.Token].AXReady || state.Handles[handle.Token].ScreenshotReady || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
		t.Fatal("failed normalization exposed observation or Artifact")
	}
	s.Artifacts().Policy.MaxBytes = 64 << 20
	success := run("success", "getAXStateAndScreenshot", handle.Token)
	if success.Error != nil || len(success.ArtifactRefs) != 1 {
		t.Fatal(success)
	}
	state, err = (&computerUseStore{b: b}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Handles[handle.Token].AXReady || !state.Handles[handle.Token].ScreenshotReady || scalar(t, s, `SELECT count(*) FROM artifact`) != 1 {
		t.Fatal("successful result missing atomic observation or Artifact")
	}
	var result map[string]any
	if json.Unmarshal([]byte(success.Text), &result) != nil || result["screenshot"].(map[string]any)["image"].(map[string]any)["artifactRef"] != success.ArtifactRefs[0] {
		t.Fatal(success)
	}
}

func (p *surfaceProvider) BrowserSnapshotResource(hash string) (computer.BrowserSnapshotter, error) {
	if hash != macosvm.BrowserSnapshotResourceHash() {
		return nil, computer.ErrCompatibility
	}
	return &p.Driver, nil
}
