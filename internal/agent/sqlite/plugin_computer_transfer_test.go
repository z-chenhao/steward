package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

type transferSurfaceProvider struct {
	surfaceProvider
	reads       int
	unknownRead bool
}

func (p *transferSurfaceProvider) ReadFile(ctx context.Context, i computer.Instance, path string) (io.ReadCloser, error) {
	p.Mu.Lock()
	p.reads++
	p.Mu.Unlock()
	if p.unknownRead {
		return nil, errors.Join(computer.ErrUnknown, errors.New("PRIVATE_EXPORT_TRANSPORT"))
	}
	return p.Driver.ReadFile(ctx, i, path)
}
func importComputerFile(t *testing.T, s *Store, r *Runs, b *codeBridge, id, path string) {
	t.Helper()
	ref := putArtifact(t, s, toolRunScope(t, s, b.req.RunID), "exact original file bytes")
	args := map[string]any{"artifactRef": ref, "destination": path}
	if _, err := computerPluginCall(t, b, id, "discover", "", "importArtifact", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approveComputerAction(t, s, r)
	result, err := computerPluginCall(t, b, id, "discover", "", "importArtifact", args)
	computerPluginValue(t, result, err)
}
func exportComputerFile(t *testing.T, s *Store, r *Runs, b *codeBridge, id string, args map[string]any) map[string]any {
	t.Helper()
	if _, err := computerPluginCall(t, b, id, "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal("export bypassed exact approval", err)
	}
	approveComputerAction(t, s, r)
	result, err := computerPluginCall(t, b, id, "discover", "", "exportArtifact", args)
	return computerPluginValue(t, result, err)
}
func TestComputerPluginTransferIndependentUpgradeRetainedRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	var h rsi.Host
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
	old := bridges[0]
	p := &transferSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	importComputerFile(t, s, r, old, "old-import", "imports/original.txt")
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, pkg := selectComputerVersion(t, h, first.Revision, "1.22.0")
	if out := exportComputerFile(t, s, r, old, "old-export", map[string]any{"path": "imports/original.txt"}); out["name"] != "original.txt" {
		t.Fatal("upgrade rewrote retained presentation", out)
	}
	if err = old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	importComputerFile(t, s, r, next, "new-import", "imports/next.txt")
	out := exportComputerFile(t, s, r, next, "new-export", map[string]any{"path": "imports/next.txt"})
	if out["name"] != "export.bin" || out["mediaType"] != "application/octet-stream" {
		t.Fatal("independent metadata not used", out)
	}
	descriptor, err := s.Artifacts().Describe(ctx, artifactOwner(t, s), out["artifactId"].(string))
	if err != nil || descriptor.Name != "export.bin" {
		t.Fatal(descriptor, err)
	}
	original := exportComputerFile(t, s, r, next, "explicit-export", map[string]any{"path": "imports/next.txt", "name": "explicit.txt", "mediaType": "text/plain"})
	if original["name"] != "explicit.txt" || original["mediaType"] != "text/plain" {
		t.Fatal("explicit metadata replaced", original)
	}
	activateDelegation(t, h, "transfer-rollback", second, first.Revision, "computer-owner-fixture")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	if out := exportComputerFile(t, s, r, next, "retired-export", map[string]any{"path": "imports/next.txt"}); out["name"] != "export.bin" {
		t.Fatal("retirement changed frozen version", out)
	}
	if err = next.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	rolled := newComputerPluginExecution(t, s, r)
	importComputerFile(t, s, r, rolled, "rolled-import", "imports/rolled.txt")
	if out := exportComputerFile(t, s, r, rolled, "rolled-export", map[string]any{"path": "imports/rolled.txt"}); out["name"] != "rolled.txt" {
		t.Fatal(out)
	}
	if _, err = h.Store.Deactivate(ctx, "transfer-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if err = rolled.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := newComputerPluginExecution(t, s, r)
	before := p.reads
	result, err := computerPluginCall(t, disabled, "disabled-export", "discover", "", "exportArtifact", map[string]any{"path": "imports/rolled.txt"})
	if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.reads != before {
		t.Fatal("deactivation reopened native export", result, err)
	}
}

func TestComputerPluginTransferPlanBeforeApprovalPreservesArtifactAndUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown", false: "approved"}[unknown], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			revision, _ := selectComputerVersion(t, h, first.Revision, "1.25.0")
			p := &transferSurfaceProvider{unknownRead: unknown}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			importComputerFile(t, s, r, b, "frozen-import", "imports/private-source")
			args := map[string]any{"path": "imports/private-source", "mediaType": "application/x-live-nondeterministic"}
			if _, err = computerPluginCall(t, b, "frozen-export", "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) || p.reads != 0 {
				t.Fatal(err, p.reads)
			}
			approvals, err := r.ApprovalView(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pending, parameters := 0, ""
			for _, approval := range approvals {
				if approval.Status == "pending" {
					pending++
					parameters = approval.Parameters
				}
			}
			if pending != 1 {
				t.Fatal("exact pending export missing; approved import history remains", approvals)
			}
			var frozen computer.ActionParameters
			if json.Unmarshal([]byte(parameters), &frozen) != nil || !strings.HasPrefix(frozen.Name, "output-") {
				t.Fatal(approvals)
			}
			facts, _ := json.Marshal(contract.ActionFacts{Method: "exportArtifact", Surface: ""})
			input, _ := json.Marshal(contract.ActionArguments{Args: computer.TransferPresentationArguments([]any{args})})
			raw, err := h.ReadService(ctx, revision, contract.TransferPresentationContract, facts, []rsi.InputData{{ID: "arguments", Data: input}})
			if err != nil {
				t.Fatal(err)
			}
			var later computer.ActionParameters
			json.Unmarshal(raw, &later)
			if frozen.Name == later.Name {
				t.Fatal("fixture did not change actual proposal", string(raw))
			}
			approveComputerAction(t, s, r)
			result, err := computerPluginCall(t, b, "frozen-export", "discover", "", "exportArtifact", args)
			if err != nil || p.reads != 1 {
				t.Fatal(result, err, p.reads)
			}
			if unknown {
				if result.Error == nil || result.Error.Code != "UNKNOWN_SIDE_EFFECT" || strings.Contains(result.Text, "PRIVATE_") {
					t.Fatal(result)
				}
			} else {
				out := computerPluginValue(t, result, err)
				if out["name"] != frozen.Name {
					t.Fatal("execution replanned approved metadata", out, frozen)
				}
				descriptor, e := s.Artifacts().Describe(ctx, artifactOwner(t, s), out["artifactId"].(string))
				if e != nil || descriptor.Name != frozen.Name {
					t.Fatal(descriptor, e)
				}
				file, e := s.Artifacts().Open(ctx, artifactOwner(t, s), out["artifactId"].(string))
				if e != nil {
					t.Fatal(e)
				}
				data, e := io.ReadAll(file)
				file.Close()
				if e != nil || string(data) != "exact original file bytes" {
					t.Fatal("metadata changed source bytes", e)
				}
			}
			result, err = computerPluginCall(t, b, "frozen-export", "discover", "", "exportArtifact", args)
			if err != nil || p.reads != 1 || scalar(t, s, `SELECT attempt_count FROM tool_invocation WHERE json_extract(arguments_json,'$.method')='exportArtifact'`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'`) != 0 {
				t.Fatal("export replayed or added pending ledger", result, err, p.reads)
			}
		})
	}
}

func TestComputerPluginTransferRejectsChangedExplicitMetadataAndInvalidDefault(t *testing.T) {
	for _, version := range []string{"1.23.0", "1.24.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, _, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, h, first.Revision, version)
			p := &transferSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			b := newComputerPluginExecution(t, s, r)
			args := map[string]any{"path": "imports/original"}
			if version == "1.23.0" {
				args["name"] = "PRIVATE_ORIGINAL.txt"
				args["mediaType"] = "text/plain"
			}
			result, err := computerPluginCall(t, b, "invalid-export", "discover", "", "exportArtifact", args)
			if err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" || p.reads != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
				t.Fatal("owner gate replaced intent or weakened artifact validation", result, err)
			}
		})
	}
}

func TestComputerPluginTransferLateForgetGuardianAndAcceptanceFailure(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine", "write-fault"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { h = installComputerPlugin(t, s, r) })
			p := &transferSurfaceProvider{}
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
					_, err = s.db.Exec(`CREATE TRIGGER fail_transfer_plan BEFORE INSERT ON computer_action_plan BEGIN SELECT RAISE(ABORT,'injected fault');END`)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			args := map[string]any{"path": "imports/PRIVATE_PATH", "name": "PRIVATE_DISPLAY.txt", "mediaType": "text/plain"}
			result, err := computerPluginCall(t, b, "late-export", "discover", "", "exportArtifact", args)
			if !crossed || p.reads != 0 || result.Text != "" || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
				t.Fatal("late metadata accepted", crossed, result, err)
			}
			if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(err)
			}
			if cause == "write-fault" {
				if !errors.Is(err, agent.ErrStorage) {
					t.Fatal(err)
				}
				s.MainBoundary = nil
				if _, err = s.db.Exec(`DROP TRIGGER fail_transfer_plan`); err != nil {
					t.Fatal(err)
				}
				if _, err = computerPluginCall(t, b, "late-export", "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) {
					t.Fatal("retry required hidden repair", err)
				}
			}
		})
	}
}

func TestComputerPluginTransferPriorEightServiceAndNativeKeepPresentation(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{true: "native", false: "eight-services"}[native], func(t *testing.T) {
			ctx := context.Background()
			var h rsi.Host
			s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) {
				if native {
					return
				}
				policy := computerOwnerPolicy(t)
				services := policy.AllowedContracts[:0]
				for _, c := range policy.AllowedContracts {
					if c != contract.BrowserProjectionContract && c != contract.BrowserUploadContract && c != contract.TransferPresentationContract {
						services = append(services, c)
					}
				}
				policy.AllowedContracts = services
				cases := policy.Cases[:0]
				for _, c := range policy.Cases {
					if c.Contract != contract.BrowserProjectionContract && c.Contract != contract.BrowserUploadContract && c.Contract != contract.TransferPresentationContract {
						cases = append(cases, c)
					}
				}
				policy.Cases = cases
				h = installRuntimePackage(t, s, r, "computer-use", policy, computerVersionPackage(t, "1.0.4"))
			})
			if native {
				h = installComputerPlugin(t, s, r)
			}
			first, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			selectComputerVersion(t, h, first.Revision, "1.22.0")
			p := &transferSurfaceProvider{}
			r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
			old := bridges[0]
			importComputerFile(t, s, r, old, "old-import", "imports/original.txt")
			if out := exportComputerFile(t, s, r, old, "old-export", map[string]any{"path": "imports/original.txt"}); out["name"] != "original.txt" || scalar(t, s, `SELECT count(*) FROM computer_action_plan`) != 0 {
				t.Fatal("new contract rewrote old consumer", out)
			}
			if scalar(t, s, `SELECT count(*) FROM rsi_run r,json_each(r.managed_services_json) j WHERE r.run_id=? AND j.value=?`, old.req.RunID, contract.TransferPresentationContract) != 0 {
				t.Fatal("old migration boundary changed")
			}
			if err = old.computer().Idle(ctx); err != nil {
				t.Fatal(err)
			}
			next := newComputerPluginExecution(t, s, r)
			importComputerFile(t, s, r, next, "new-import", "imports/new.txt")
			if out := exportComputerFile(t, s, r, next, "new-export", map[string]any{"path": "imports/new.txt"}); out["name"] != "export.bin" {
				t.Fatal("new Run missed new method", out)
			}
		})
	}
}

func TestComputerPluginTransferApprovalDoesNotGrantAnotherRunFile(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &transferSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	old := bridges[0]
	importComputerFile(t, s, r, old, "source-import", "imports/known")
	if err := old.computer().Idle(ctx); err != nil {
		t.Fatal(err)
	}
	next := newComputerPluginExecution(t, s, r)
	args := map[string]any{"path": "imports/known"}
	if _, err := computerPluginCall(t, next, "foreign-export", "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	approveComputerAction(t, s, r)
	result, err := computerPluginCall(t, next, "foreign-export", "discover", "", "exportArtifact", args)
	if err != nil || result.Error == nil || result.Error.Code != "PERMISSION_DENIED" || p.reads != 0 {
		t.Fatal("metadata approval manufactured a file grant", result.Error, err, p.reads)
	}
}

func TestComputerPluginTransferControlledApprovalRecoveryKeepsSourceAndPlan(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, parents := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &transferSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	importComputerFile(t, s, r, b, "recover-import", "imports/original-recover.bin")
	args := map[string]any{"path": "imports/original-recover.bin"}
	if _, err := computerPluginCall(t, b, "recover-export", "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) {
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
	// Immediately exercise the original source/path after the real claim, without
	// extra selection or SQL repair of readiness, grants, state or method choice.
	result, err := computerPluginCall(t, &next, "recover-export", "discover", "", "exportArtifact", args)
	out := computerPluginValue(t, result, err)
	if out["name"] != "original-recover.bin" || out["mediaType"] != "application/octet-stream" || p.reads != 1 {
		t.Fatal("recovery lost original export", out, p.reads)
	}
	var afterHash string
	if err = s.db.QueryRow(`SELECT plan_hash FROM computer_action_plan`).Scan(&afterHash); err != nil || afterHash != beforeHash {
		t.Fatal("plan changed after recovery", err)
	}
	after, err := s.ComputerStatus(ctx)
	if err != nil || after.ComputerID != before.ComputerID || after.Epoch != before.Epoch {
		t.Fatal("controlled approval replaced source instance", before, after, err)
	}
	if err = b.computerStore().Fence(ctx); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old execution retained authority", err)
	}
}

func TestComputerPluginTransferForgetErasesResolvedNameAndRetainsAudit(t *testing.T) {
	ctx := context.Background()
	s, r, bridges, _ := computerPluginClaims(t, func(s *Store, r *Runs) { installComputerPlugin(t, s, r) })
	p := &transferSurfaceProvider{}
	r.ConfigureComputer(p, computer.Compatibility{}, computer.RestorationManifest{Version: 1})
	b := bridges[0]
	args := map[string]any{"path": "imports/PRIVATE_RESOLVED_NAME"}
	if _, err := computerPluginCall(t, b, "private-export", "discover", "", "exportArtifact", args); !errors.Is(err, tool.ErrSuspended) {
		t.Fatal(err)
	}
	var hash, body string
	if err := s.db.QueryRow(`SELECT plan_hash,plan_json FROM computer_action_plan`).Scan(&hash, &body); err != nil || !strings.Contains(body, "PRIVATE_RESOLVED_NAME") {
		t.Fatal(body, err)
	}
	owner := idAgent(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	var afterHash string
	var erased sql.NullString
	if err := s.db.QueryRow(`SELECT plan_hash,plan_json FROM computer_action_plan`).Scan(&afterHash, &erased); err != nil || erased.Valid || afterHash != hash {
		t.Fatal("Forget did not erase presentation one-way", erased, err)
	}
	if _, err := s.db.Exec(`UPDATE computer_action_plan SET plan_json=?`, body); err == nil {
		t.Fatal("erased body revived")
	}
	approvals, err := r.ApprovalView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range approvals {
		if strings.Contains(a.Parameters, "PRIVATE_RESOLVED_NAME") {
			t.Fatal("erased parameter body disclosed")
		}
	}
	if result, err := computerPluginCall(t, b, "private-export", "discover", "", "exportArtifact", args); !errors.Is(err, agent.ErrClaimLost) || p.reads != 0 || result.Text != "" {
		t.Fatal("revoked export executed", result, err)
	}
}
