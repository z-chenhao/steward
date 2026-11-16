package rsi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/media/ocr"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/driver"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func ocrPackageSource(t *testing.T) string {
	return ocrPackageVariant(t, false, false)
}

func ocrPackageVariant(t *testing.T, repeat, noPermission bool) string {
	return ocrPackageVersion(t, "1.0.1", repeat, noPermission)
}
func ocrPackageVersion(t *testing.T, version string, repeat, noPermission bool) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/image-ocr")
	if repeat || version != "1.0.1" {
		path, err := filepath.Abs("../plugin/imageocr/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		needle := `data, err := rsiplugin.Compute(ctx, contract.EngineContract, contract.EngineRequest{InputID: req.InputID, MediaType: req.MediaType, Languages: req.Languages, Layout: layout}, &meta)`
		code := string(raw)
		if repeat {
			code = strings.Replace(code, needle, `_, _ = rsiplugin.Compute(ctx, contract.EngineContract, contract.EngineRequest{InputID: req.InputID, MediaType: req.MediaType, Languages: req.Languages, Layout: layout}, &meta)`+"\n"+needle, 1)
		}
		code = strings.Replace(code, `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
		if code == string(raw) {
			t.Fatal("repeat fixture patch missing")
		}
		override := filepath.Join(t.TempDir(), "plugin.go")
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
		overlayPath := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("go", "build", "-overlay", overlayPath, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/image-ocr")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/image-ocr/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = version
	if noPermission {
		m.Requests = []string{"input.read"}
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

func TestImageOCRComputeCannotExpandAuthority(t *testing.T) {
	requireSandbox(t)
	for _, tc := range []struct {
		name                 string
		repeat, noPermission bool
		wantCalls            int
	}{{"missing permission", false, true, 0}, {"second computation", true, false, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			pkg, err := f.host.Install(context.Background(), ocrPackageVariant(t, tc.repeat, tc.noPermission))
			if err != nil {
				t.Fatal(err)
			}
			binding := rsiplugin.Binding{InstanceID: "ocr", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "ocr", StateVersion: 1}
			calls := 0
			runner := rsi.Runner{Catalog: f.host.Catalog, Compute: func(context.Context, rsiplugin.ComputeRequest) (rsiplugin.ComputeResult, error) {
				calls++
				return rsiplugin.ComputeResult{Output: json.RawMessage(`{"width":0,"height":0,"languages":[],"error_code":"PROVIDER_UNAVAILABLE"}`)}, nil
			}}
			var result rsiplugin.Result
			err = runner.Invoke(context.Background(), binding, "call", rsiplugin.Call{Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"image","media_type":"image/png","languages":[]}`), Inputs: []rsiplugin.Input{{ID: "image", Size: 0, Digest: rsiplugin.FileDigest(nil)}}, State: rsiplugin.State{Value: json.RawMessage(`{}`)}}, &result)
			if err == nil || calls != tc.wantCalls {
				t.Fatal("computation escaped permission/budget", calls, err)
			}
		})
	}
}

func TestImageOCRActualPluginNativeToolAndLargeResult(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	engine := ocr.Discover(ctx)
	if !engine.Available() {
		t.Skip("actual Tesseract/language data unavailable; normalization has separate coverage")
	}
	f := newFixture(t, ocr.Definition(engine.ResourceHash()))
	f.runs.ConfigureImageOCR(engine)
	f.host.Compute = driver.New(engine)
	pixels, err := os.ReadFile("../media/ocr/testdata/text.png")
	if err != nil {
		t.Fatal(err)
	}
	staged, err := f.store.Artifacts().Stage(ctx, artifact.Input{Name: "pixels.png", MediaType: "image/png"}, bytes.NewReader(pixels))
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	diagnostics, _ := f.store.Diagnostics(ctx)
	var ref string
	if err = f.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		ref, err = f.store.Artifacts().CommitTx(ctx, tx, artifact.Scope{Owner: true, UserID: f.store.OwnerID(), AgentID: diagnostics.AgentID}, staged, "ocr-pixels")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	parts := []conversation.ContentPart{{Type: "text", Text: "识别图片文字"}, {Type: "image", ArtifactRef: ref}}
	legacy, err := f.runs.Submit(ctx, "legacy-ocr", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), OCR: ocrPackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	status, _ := f.host.Store.Status(ctx)
	if status.Epoch != 1 {
		t.Fatal(status)
	}
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		for _, m := range req.Messages {
			if m.Role == "tool" {
				return llm.GenerationResult{Text: "OCR finished"}
			}
		}
		args, _ := json.Marshal(map[string]string{"artifactRef": tool.ObjectID(ref)})
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "ocr", Name: ocr.Name, Arguments: string(args)}}}
	}}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=?`, legacy.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy OCR interpreter changed", count, err)
	}
	run, err := f.runs.Submit(ctx, "managed-ocr", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var receipt string
	if err = f.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, run.RunID, ocr.Name).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if json.Unmarshal([]byte(receipt), &result) != nil || result.Error != nil || !strings.Contains(result.Text, "OCR 12345") || !strings.Contains(result.Text, "document_ref") {
		t.Fatal("native tool did not use actual plugin/engine", receipt)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='image-ocr' AND result_json IS NOT NULL`, run.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing managed OCR receipt", count, err)
	}
	testOCRLargeResult(t, f, run.RunID)
	first, err := f.host.Store.Graph(ctx, status.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := f.host.Install(ctx, ocrPackageVersion(t, "1.1.0", false, false))
	if err != nil {
		t.Fatal(err)
	}
	updated := first.Graph
	updated.Bindings = append([]rsiplugin.Binding(nil), updated.Bindings...)
	for i, b := range updated.Bindings {
		if b.InstanceID == "image-ocr" {
			updated.Bindings[i].PackageDigest = p2.Digest
		}
	}
	second, err := f.host.Prepare(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	var policyID string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	selectVersion := func(id, baseline, target string, checkDrift bool) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policyID, Goal: "independent OCR compatible version"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		selection := rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}
		if checkDrift {
			other := f.host
			compute := *other.Compute
			compute.Identity = func(context.Context) (string, error) { return "changed-native-resource", nil }
			other.Compute = &compute
			if _, err := other.Activate(ctx, selection); !errors.Is(err, rsi.ErrVerification) {
				t.Fatal("native resource drift used old evidence", err)
			}
		}
		if _, err := f.host.Activate(ctx, selection); err != nil {
			t.Fatal(err)
		}
	}
	selectVersion("upgrade-ocr", first.Revision, second.Revision, true)
	newRun, err := f.runs.Submit(ctx, "ocr-after-upgrade", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	selectVersion("rollback-ocr", second.Revision, first.Revision, false)
	if err = f.host.Store.Retire(ctx, p2.Digest); err != nil {
		t.Fatal(err)
	}
	imageInput, _ := json.Marshal(contract.Request{InputID: "image", MediaType: "image/png", Languages: engine.Languages()})
	for _, id := range []string{run.RunID, newRun.RunID} {
		value, err := f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: id}, "image-ocr", contract.Contract, "retained-after-rollback", imageInput, []rsi.InputData{{ID: "image", Data: pixels}})
		if err != nil || !strings.Contains(string(value), "OCR 12345") {
			t.Fatal("old OCR binary/resources lost", string(value), err)
		}
	}
	if _, err = f.host.Store.Deactivate(ctx, "disable-ocr", status.Revision); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.runs.Submit(ctx, "disabled-ocr", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, disabled.RunID, ocr.Name).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(receipt), &result) != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("disabled plugin fell back to native OCR", receipt)
	}
	if err = f.host.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: run.RunID}, "image-ocr", contract.Contract, "after-quarantine", imageInput, []rsi.InputData{{ID: "image", Data: pixels}}); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("quarantine bypassed", err)
	}
}

func TestOCRPluginLargeResultWithoutNativeEngine(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), OCR: ocrPackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	run, err := f.actor.Submit(ctx, "large-normalized-ocr", "offline normalization")
	if err != nil {
		t.Fatal(err)
	}
	testOCRLargeResult(t, f, run.RunID)
}
func testOCRLargeResult(t *testing.T, f *fixture, runID string) {
	t.Helper()
	ctx := context.Background()
	// A legal legacy document may serialize above one frame because control
	// characters require JSON escaping. Result chunks preserve that behavior.
	header := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
	row := "5\t1\t1\t1\t1\t1\t4\t8\t20\t10\t98.5\t" + strings.Repeat("\x00", 30) + "\n"
	tsv := []byte(header + strings.Repeat(row, 4096))
	input := json.RawMessage(`{"input_id":"tsv","width":100,"height":50,"languages":["eng"]}`)
	value, err := f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: runID}, "image-ocr", contract.TSVContract, "large-result", input, []rsi.InputData{{ID: "tsv", Data: tsv}})
	var response contract.Response
	if err != nil || len(value) <= rsiplugin.MaxFrameBytes || rsiplugin.DecodeResult(value, &response) != nil || response.Document == nil || len(response.Document.Words) != 4096 {
		t.Fatal("bounded OCR result lost", len(value), err)
	}
	again, err := f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: runID}, "image-ocr", contract.TSVContract, "large-result", input, []rsi.InputData{{ID: "tsv", Data: tsv}})
	if err != nil || !bytes.Equal(again, value) {
		t.Fatal("large result replay changed", err)
	}
	// This oracle is built from the independent fixture specification, never
	// from candidate output. Logical policies/results can exceed one RPC frame.
	expected := contract.Document{Method: "ocr", Languages: []string{"eng"}, Width: 100, Height: 50, Text: strings.TrimSuffix(strings.Repeat(strings.Repeat("\x00", 30)+" ", 4096), " "), Words: []contract.Word{}}
	for range 4096 {
		expected.Words = append(expected.Words, contract.Word{Text: strings.Repeat("\x00", 30), X: 4, Y: 8, Width: 20, Height: 10, Confidence: 98.5})
	}
	want, _ := json.Marshal(contract.Response{Document: &expected})
	status, _ := f.host.Store.Status(ctx)
	var policyID string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE candidate_id=?`, "bootstrap-docx-"+strings.TrimPrefix(status.Revision, "sha256:")).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	policy, err := f.host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "large-ocr-independent"
	policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "image-ocr", Contract: contract.TSVContract, Input: input, Inputs: []rsi.InputData{{ID: "tsv", Data: tsv}}, Expected: want})
	if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal("large independent policy rejected", err)
	}
	if f.host.Compute == nil {
		f.host.Compute = driver.New(nil)
	}
	if err = f.host.Propose(ctx, rsi.Candidate{ID: "large-ocr-policy", Baseline: status.Revision, Target: status.Revision, PolicyID: policy.ID, Goal: "Preserve legal document result and independent evidence beyond one frame"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Verify(ctx, "large-ocr-policy"); err != nil {
		t.Fatal("large independent expected result could not be verified", err)
	}
}
