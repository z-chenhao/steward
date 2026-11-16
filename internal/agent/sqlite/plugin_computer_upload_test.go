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
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type uploadSurfaceProvider struct {
	surfaceProvider
	uploads       []computer.BrowserUploadRequest
	bytes         [][]byte
	legacy        int
	unknownUpload bool
}

func (p *uploadSurfaceProvider) BrowserUploadResource(hash string) (computer.BrowserUploader, error) {
	if hash != macosvm.BrowserUploadResourceHash() {
		return nil, computer.ErrCompatibility
	}
	return p, nil
}
func (p *uploadSurfaceProvider) Upload(ctx context.Context, i computer.Instance, target computer.Target, lease string, a computer.BrowserUploadRequest) (any, error) {
	if a.Validate() != nil || target.Kind != "tab" || target.ID != "fixture-tab" || lease == "" {
		return nil, computer.ErrInvalid
	}
	p.Mu.Lock()
	defer p.Mu.Unlock()
	data, ok := p.Files[a.Path]
	if !ok {
		return nil, computer.ErrIntegrity
	}
	p.uploads = append(p.uploads, a)
	p.bytes = append(p.bytes, append([]byte(nil), data...))
	if p.unknownUpload {
		return nil, errors.Join(computer.ErrUnknown, errors.New("PRIVATE_UPLOAD_TRANSPORT"))
	}
	return map[string]any{"effect": "dispatched"}, nil
}
func (p *uploadSurfaceProvider) Act(ctx context.Context, i computer.Instance, target computer.Target, lease, method string, args map[string]any) (any, error) {
	if method == "playwright.upload" {
		p.Mu.Lock()
		p.legacy++
		p.Mu.Unlock()
	}
	return p.surfaceProvider.Act(ctx, i, target, lease, method, args)
}
func computerUploadArgs(t *testing.T, s *Store, path, selector string) map[string]any {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`SELECT computer_id FROM computer`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"selector": selector, "file": map[string]any{"computerId": id, "path": path}}
}
func uploadComputerFile(t *testing.T, s *Store, r *Runs, b *codeBridge, cap, id, path string) {
	t.Helper()
	args := computerUploadArgs(t, s, path, "#upload")
	if _, err := computerPluginCall(t, b, id, "invoke", cap, "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("upload bypassed original approval", err)
	}
	approveComputerAction(t, s, r)
	result, err := computerPluginCall(t, b, id, "invoke", cap, "playwright.upload", args)
	computerPluginValue(t, result, err)
}
func TestComputerPluginUploadIndependentUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	p := &uploadSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	old := bridges[0]
	importComputerFile(t, s, r, old, "old-import", "imports/old.bin")
	cap := playwrightCapability(t, old)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, pkg := selectComputerVersion(t, h, first.Revision, "1.26.0")
	uploadComputerFile(t, s, r, old, cap, "old-upload", "imports/old.bin")
	if p.uploads[0].Name != "old.bin" || p.uploads[0].MediaType != "application/octet-stream" {
		t.Fatal("retained graph replaced", p.uploads)
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	importComputerFile(t, s, r, next, "new-import", "imports/new.bin")
	cap = playwrightCapability(t, next)
	uploadComputerFile(t, s, r, next, cap, "new-upload", "imports/new.bin")
	if p.uploads[1].Name != "chosen-upload.txt" || p.uploads[1].MediaType != "text/plain" || string(p.bytes[1]) != "exact original file bytes" || p.legacy != 0 {
		t.Fatal("independent method not dispatched", p.uploads, p.legacy)
	}
	activateDelegation(t, h, "upload-rollback", second, first.Revision, "computer-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	uploadComputerFile(t, s, r, next, cap, "retired-upload", "imports/new.bin")
	if p.uploads[2].Name != "chosen-upload.txt" {
		t.Fatal("retirement replaced original choice", p.uploads)
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	rolled := newComputerPluginExecution(t, s, r)
	importComputerFile(t, s, r, rolled, "rolled-import", "imports/rolled.bin")
	cap = playwrightCapability(t, rolled)
	uploadComputerFile(t, s, r, rolled, cap, "rolled-upload", "imports/rolled.bin")
	if p.uploads[3].Name != "rolled.bin" {
		t.Fatal(p.uploads)
	}
	if _, err = h.Store.Deactivate(ctx, "upload-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if err = rolled.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	result, err := computerPluginCall(t, disabled, "disabled-upload", "invoke", cap, "playwright.upload", map[string]any{"selector": "#upload", "file": map[string]any{"path": "imports/rolled.bin"}})
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.uploads) != 4 || p.legacy != 0 {
		t.Fatal("disable restored native upload", result, err, p.legacy)
	}
}
func TestComputerPluginUploadPlanBeforeApprovalPreservesBytesAndUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "approved", true: "unknown"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := selectComputerVersion(t, h, first.Revision, "1.29.0")
			p := &uploadSurfaceProvider{unknownUpload: unknown}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			path := "imports/PRIVATE_UPLOAD_SOURCE.bin"
			importComputerFile(t, s, r, b, "frozen-import", path)
			cap := playwrightCapability(t, b)
			args := computerUploadArgs(t, s, path, "#LIVE_NONDETERMINISTIC")
			args["timeoutMs"] = 1750
			if _, err = computerPluginCall(t, b, "frozen-upload", "invoke", cap, "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) || len(p.uploads) != 0 {
				t.Fatal(err, p.uploads)
			}
			views, err := r.ApprovalView(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var frozen computer.ActionParameters
			pending := 0
			for _, v := range views {
				if v.Status == "pending" {
					pending++
					if json.Unmarshal([]byte(v.Parameters), &frozen) != nil {
						t.Fatal(v)
					}
				}
			}
			if pending != 1 || !strings.HasPrefix(frozen.Name, "upload-") || frozen.Selector != "#LIVE_NONDETERMINISTIC" || *frozen.TimeoutMS != 1750 {
				t.Fatal("complete plan missing before approval", views)
			}
			facts, _ := json.Marshal(contract.ActionFacts{Method: "playwright.upload", Surface: "tab"})
			data, _ := json.Marshal(contract.ActionArguments{Args: computer.PlaywrightParameterArguments("playwright.upload", []any{args})})
			raw, err := h.ReadService(ctx, revision, contract.BrowserUploadContract, facts, []rsi.InputData{{ID: "arguments", Data: data}})
			if err != nil {
				t.Fatal(err)
			}
			var later computer.ActionParameters
			json.Unmarshal(raw, &later)
			if later.Name == frozen.Name {
				t.Fatal("independent process did not change choice", string(raw))
			}
			approveComputerAction(t, s, r)
			result, err := computerPluginCall(t, b, "frozen-upload", "invoke", cap, "playwright.upload", args)
			if err != nil || len(p.uploads) != 1 || p.uploads[0].Name != frozen.Name || p.uploads[0].Path != path || p.uploads[0].TimeoutMS != 1750 || string(p.bytes[0]) != "exact original file bytes" || p.legacy != 0 {
				t.Fatal(result, err, p.uploads, p.legacy)
			}
			if unknown {
				if result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || strings.Contains(result.Text, "PRIVATE_") {
					t.Fatal(result)
				}
			} else {
				computerPluginValue(t, result, err)
			}
			result, err = computerPluginCall(t, b, "frozen-upload", "invoke", cap, "playwright.upload", args)
			if err != nil || len(p.uploads) != 1 || scalar(t, s, `SELECT attempt_count FROM tool_invocation WHERE json_extract(arguments_json,'$.method')='playwright.upload'`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
				t.Fatal("upload effect retried", result, err, p.uploads)
			}
		})
	}
}
func TestComputerPluginUploadRejectsChangedSelectorAndInvalidMetadata(t *testing.T) {
	for _, version := range []string{"1.27.0", "1.28.0", "1.30.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, _ := h.Store.Status(ctx)
			selectComputerVersion(t, h, first.Revision, version)
			p := &uploadSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			importComputerFile(t, s, r, b, "invalid-import", "imports/original.bin")
			cap := playwrightCapability(t, b)
			before := scalar(t, s, `SELECT count(*) FROM approval`)
			plans := scalar(t, s, `SELECT count(*) FROM computer_action_plan`)
			result, err := computerPluginCall(t, b, "invalid-upload", "invoke", cap, "playwright.upload", computerUploadArgs(t, s, "imports/original.bin", "#LIVE_ORIGINAL"))
			if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || len(p.uploads) != 0 || p.legacy != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != before || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != plans {
				t.Fatal("semantic owner gate bypassed", result, err, p.uploads)
			}
		})
	}
}
func TestComputerPluginUploadUnknownResourceRejectedBeforeApprovalOrEnsure(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	graph := original.Graph
	graph.Bindings[0].Config = json.RawMessage(`{}`)
	if _, err = h.Prepare(ctx, graph); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("missing frozen resource accepted", err)
	}
	graph.Bindings[0].Config, _ = json.Marshal(map[string]string{"browser_upload_driver": strings.Repeat("0", 64), "browser_snapshot_driver": macosvm.BrowserSnapshotResourceHash()})
	prepared, err := h.Prepare(ctx, graph)
	if err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "unknown-upload-resource", first.Revision, prepared.Revision, "computer-owner-fixture")
	p := &uploadSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := newComputerPluginExecution(t, s, r)
	result, err := computerPluginCall(t, b, "unknown-resource", "invoke", "no-surface", "playwright.upload", map[string]any{"selector": "#upload", "file": map[string]any{"path": "imports/private"}})
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.ensures != 0 || len(p.uploads) != 0 || p.legacy != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
		t.Fatal("config data selected an untrusted program", result, err, p.ensures)
	}
}

func TestComputerPluginUploadPriorNineServiceAndNativeRemainUnmanaged(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "nine-services", true: "native"}[native], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if native {
					return
				}
				policy := computerOwnerPolicy(t)
				allowed := policy.AllowedContracts[:0]
				for _, c := range policy.AllowedContracts {
					if c != contract.BrowserProjectionContract && c != contract.BrowserUploadContract {
						allowed = append(allowed, c)
					}
				}
				policy.AllowedContracts = allowed
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				h = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.5"))
			})
			if native {
				h = installComputerPlugin(t, s, r)
			}
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, h, first.Revision, "1.26.0")
			p := &uploadSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			old := bridges[0]
			importComputerFile(t, s, r, old, "legacy-import", "imports/legacy.bin")
			cap := playwrightCapability(t, old)
			uploadComputerFile(t, s, r, old, cap, "legacy-upload", "imports/legacy.bin")
			if len(p.uploads) != 0 || p.legacy != 1 || scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) j WHERE r.run_id=? AND j.value=?`, old.req.RunID, contract.BrowserUploadContract) != 0 {
				t.Fatal("new service backfilled native/historical graph", p.uploads, p.legacy)
			}
			if err = old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			importComputerFile(t, s, r, next, "current-import", "imports/current.bin")
			cap = playwrightCapability(t, next)
			uploadComputerFile(t, s, r, next, cap, "current-upload", "imports/current.bin")
			if len(p.uploads) != 1 || p.uploads[0].Name != "chosen-upload.txt" || p.legacy != 1 {
				t.Fatal("current graph used historical primitive", p.uploads, p.legacy)
			}
		})
	}
}
func TestComputerPluginUploadLateForgetGuardianAndAcceptanceFailure(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine", "write-fault"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			p := &uploadSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := bridges[0]
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
					_, err = s.db.Exec(`CREATE TRIGGER fail_upload_plan BEFORE INSERT ON computer_action_plan BEGIN SELECT RAISE(ABORT,'injected fault');END`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			args := map[string]any{"selector": "#upload", "file": map[string]any{"computerId": "private-instance", "path": "imports/PRIVATE_UPLOAD_NAME"}}
			result, err := computerPluginCall(t, b, "late-upload", "invoke", "no-surface", "playwright.upload", args)
			if !crossed || len(p.uploads) != 0 || p.legacy != 0 || p.ensures != 0 || result.Text != "" || scalar(t, s, `SELECT count(*) FROM approval`) != 0 || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
				t.Fatal("late method result accepted", result, err, crossed, p.ensures)
			}
			if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(err)
			}
			if cause == "write-fault" {
				if !errors.Is(err, agent.ErrStorage) {
					t.Fatal(err)
				}
				s.MainBoundary = nil
				if _, err = s.db.Exec(`DROP TRIGGER fail_upload_plan`); err != nil {
					t.Fatal(err)
				}
				if _, err = computerPluginCall(t, b, "late-upload", "invoke", "no-surface", "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) {
					t.Fatal("retry required hidden repair", err)
				}
			}
		})
	}
}
func TestComputerPluginUploadControlledApprovalRecoveryKeepsResourceAndPlan(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, parents := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	p := &uploadSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	importComputerFile(t, s, r, b, "recover-import", "imports/recover-upload.bin")
	cap := playwrightCapability(t, b)
	args := computerUploadArgs(t, s, "imports/recover-upload.bin", "#upload")
	if _, err := computerPluginCall(t, b, "recover-upload", "invoke", cap, "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	var beforeHash string
	if err := s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&beforeHash); err != nil {
		t.Fatal(err)
	}
	before, err := s.ComputerStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := h.Store.Graph(ctx, selected.Revision)
	if err != nil {
		t.Fatal(err)
	}
	original.Graph.Bindings[0].Config, _ = json.Marshal(map[string]string{"browser_upload_driver": strings.Repeat("0", 64), "browser_snapshot_driver": macosvm.BrowserSnapshotResourceHash()})
	changed, err := h.Prepare(ctx, original.Graph)
	if err != nil {
		t.Fatal(err)
	}
	activateDelegation(t, h, "resource-different-from-retained-run", selected.Revision, changed.Revision, "computer-owner-fixture")
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
	// The first interaction after the actual claim uses the original capability,
	// source, graph resource and approved plan; no extra select/reload/SQL repair.
	result, err := computerPluginCall(t, &next, "recover-upload", "invoke", cap, "playwright.upload", args)
	computerPluginValue(t, result, err)
	if len(p.uploads) != 1 || p.uploads[0].Name != "recover-upload.bin" || p.uploads[0].Path != "imports/recover-upload.bin" || string(p.bytes[0]) != "exact original file bytes" || p.legacy != 0 {
		t.Fatal("controlled recovery changed resource or intent", p.uploads)
	}
	var afterHash string
	if err = s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&afterHash); err != nil || afterHash != beforeHash {
		t.Fatal(afterHash, err)
	}
	after, err := s.ComputerStatus(ctx)
	if err != nil || after.ComputerID != before.ComputerID || after.Epoch != before.Epoch {
		t.Fatal(before, after, err)
	}
	if err = b.computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old execution retained authority", err)
	}
}
func TestComputerPluginUploadForgetErasesResolvedNameAndRetainsAudit(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &uploadSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	args := map[string]any{"selector": "#upload", "file": map[string]any{"computerId": "private-instance", "path": "imports/PRIVATE_RESOLVED_UPLOAD_NAME"}}
	if _, err := computerPluginCall(t, b, "private-upload", "invoke", "no-surface", "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	var hash, body string
	if err := s.db.QueryRow(`SELECT plan_hash,plan_json FROM computer_action_plan`).Scan(&hash, &body); err != nil || !strings.Contains(body, "PRIVATE_RESOLVED_UPLOAD_NAME") {
		t.Fatal(body, err)
	}
	owner := idAgent(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	var afterHash string
	var erased sql.NullString
	if err := s.db.QueryRow(`SELECT plan_hash,plan_json FROM computer_action_plan`).Scan(&afterHash, &erased); err != nil || erased.Valid || afterHash != hash {
		t.Fatal("Forget did not erase metadata one-way", erased, err)
	}
	if _, err := s.db.Exec(`UPDATE computer_action_plan SET plan_json=?`, body); err == nil {
		t.Fatal("erased upload plan revived")
	}
	views, err := r.ApprovalView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if strings.Contains(v.Parameters, "PRIVATE_RESOLVED_UPLOAD_NAME") {
			t.Fatal("erased metadata disclosed")
		}
	}
	if result, err := computerPluginCall(t, b, "private-upload", "invoke", "no-surface", "playwright.upload", args); !errors.Is(err, agent.ErrClaimLost) || len(p.uploads) != 0 || p.legacy != 0 || p.ensures != 0 || result.Text != "" {
		t.Fatal(result, err, p.uploads)
	}
}
func TestComputerPluginUploadApprovalDoesNotGrantAnotherRunFile(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &uploadSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	old := bridges[0]
	importComputerFile(t, s, r, old, "owned-import", "imports/known-upload.bin")
	if err := old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	cap := playwrightCapability(t, next)
	args := computerUploadArgs(t, s, "imports/known-upload.bin", "#upload")
	if _, err := computerPluginCall(t, next, "foreign-upload", "invoke", cap, "playwright.upload", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approveComputerAction(t, s, r)
	result, err := computerPluginCall(t, next, "foreign-upload", "invoke", cap, "playwright.upload", args)
	if err != nil || result.Error == nil || result.Error.Code != "PERMISSION_DENIED" || len(p.uploads) != 0 || p.legacy != 0 {
		t.Fatal("metadata approval created another Run's file grant", result, err)
	}
}
