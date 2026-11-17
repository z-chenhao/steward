package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/driver"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func tracePackageSource(t *testing.T) string { return tracePackageVersion(t, "1.0.1", false) }
func tracePackageVersion(t *testing.T, version string, block bool) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/trace-inspector")
	if version != "1.1.0" || block {
		path, err := filepath.Abs("../plugin/traceinspector/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(raw), `Version: "1.1.0"`, `Version: "`+version+`"`, 1)
		if version == "1.0.1" {
			original, e := os.ReadFile("../../tests/fixtures/plugins/trace-workbench-original-v1/internal__plugin__traceinspector__plugin.go.txt")
			if e != nil {
				t.Fatal(e)
			}
			code = string(original)
		}
		if block {
			code = strings.Replace(code, `"context"`, `"context";"os";"time"`, 1)
			needle := `raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])`
			code = strings.Replace(code, needle, `if call.OperationID=="owner-read"{if err:=os.WriteFile("trace-ready",[]byte("ready"),0600);err!=nil{return rsiplugin.Result{},err};for{if _,err:=os.Stat("resume");err==nil{break};select{case <-ctx.Done():return rsiplugin.Result{},ctx.Err();case <-time.After(5*time.Millisecond):}}}`+"\n"+needle, 1)
		}
		if code == string(raw) {
			t.Fatal("trace overlay unchanged")
		}
		override := filepath.Join(t.TempDir(), "plugin.go")
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements := map[string]string{path: override}
		if version == "1.0.1" {
			contractPath, e := filepath.Abs("../plugin/traceinspector/contract/contract.go")
			if e != nil {
				t.Fatal(e)
			}
			original, e := os.ReadFile("../../tests/fixtures/plugins/trace-workbench-original-v1/internal__plugin__traceinspector__contract__contract.go.txt")
			if e != nil {
				t.Fatal(e)
			}
			oldContract := filepath.Join(t.TempDir(), "contract.go")
			if e = os.WriteFile(oldContract, original, 0600); e != nil {
				t.Fatal(e)
			}
			replacements[contractPath] = oldContract
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{replacements})
		file := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(file, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/trace-inspector")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/trace-inspector/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if version == "1.0.1" {
		raw, err = os.ReadFile("../../tests/fixtures/plugins/trace-workbench-original-v1/plugins__trace-inspector__steward.plugin.json.txt")
		if err != nil {
			t.Fatal(err)
		}
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(source, "views"), 0700); err != nil {
		t.Fatal(err)
	}
	program, readErr := os.ReadFile("../plugin/traceinspector/view/trace-worker.js")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err = os.WriteFile(filepath.Join(source, "views", "trace-worker.js"), program, 0600); err != nil {
		t.Fatal(err)
	}
	if version != "1.0.1" {
		style, e := os.ReadFile("../plugin/traceinspector/view/trace-activity.css")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "views", "trace-activity.css"), style, 0600); e != nil {
			t.Fatal(e)
		}
		copySource, e := os.ReadFile("../plugin/traceinspector/view/copy.json")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "views", "copy.json"), copySource, 0600); e != nil {
			t.Fatal(e)
		}
		view, e := os.ReadFile("../plugin/traceinspector/view/trace.html")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "views", "trace.html"), view, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m.Version = version
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

func TestTracePluginProjectsOwnerEvidenceWithoutWritingLedger(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		for _, m := range req.Messages {
			if m.Role == "tool" {
				return llm.GenerationResult{Text: "completed diagnostic generation"}
			}
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "trace-test-echo", Name: "mcp__fixture__echo", Arguments: `{"text":"private owner body"}`}}}
	}}
	run, err := f.actor.Submit(ctx, "trace-old-reference", "private owner diagnostic input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	inspector := f.store.Inspector()
	inspector.ConfigureTracePlugins(f.host.Catalog)
	query := observability.ObservationQuery{RunID: run.RunID}
	diffQuery := observability.DiffQuery{RunID: run.RunID, Step: 1, Section: "messages"}
	legacyDiff, err := inspector.ContextDiff(ctx, diffQuery)
	if err != nil || len(legacyDiff.Items) == 0 {
		t.Fatal("missing frozen diff baseline", legacyDiff, err)
	}
	_, view, err := inspector.ProjectedObservations(ctx, query)
	if err != nil || view != nil {
		t.Fatal("pre-migration inspector changed", view, err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), OCR: ocrPackageSource(t), Trace: tracePackageSource(t)}); err != nil {
		t.Fatal(err)
	}
	status, _ := f.host.Store.Status(ctx)
	counts := func() []int {
		t.Helper()
		v := make([]int, 3)
		for i, table := range []string{"rsi_call", "rsi_state", "rsi_audit"} {
			if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&v[i]); err != nil {
				t.Fatal(err)
			}
		}
		return v
	}
	before := counts()
	legacySelection, err := inspector.TraceWorkbenchSelection(ctx, "")
	if err != nil || legacySelection.Managed || legacySelection.ReaderRevision != status.Revision {
		t.Fatal("original 1.0.1 page boundary lost", legacySelection, err)
	}
	data, view, err := inspector.ProjectedObservations(ctx, query)
	if err != nil || view == nil || len(view.Rows) == 0 {
		t.Fatal("owner inspector did not use real package", view, err)
	}
	sources := map[string]observability.Observation{}
	for _, o := range data.Items {
		sources[o.ID] = o
	}
	for _, row := range view.Rows {
		if !reflect.DeepEqual(row.Observation, sources[row.ID]) || row.Depth > 16 || row.X < 0 || row.X+row.Width > 1000 {
			t.Fatal("projection rewrote domain evidence", row)
		}
	}
	managedDiff, err := inspector.ContextDiff(ctx, diffQuery)
	if managedDiff.ReaderRevision != status.Revision {
		t.Fatal("diff pagination did not pin interpreter", managedDiff.ReaderRevision)
	}
	managedDiff.ReaderRevision = ""
	if err != nil || !reflect.DeepEqual(managedDiff, legacyDiff) {
		t.Fatal("versioned diff changed frozen comparison", managedDiff, legacyDiff, err)
	}
	var toolNode string
	for _, o := range data.Items {
		if o.Type == "tool" {
			toolNode = o.ID
			break
		}
	}
	if toolNode == "" {
		t.Fatal("missing actual tool evidence")
	}
	body, preview, managed, err := inspector.ProjectedToolOutput(ctx, observability.BodyQuery{ObservationQuery: observability.ObservationQuery{RunID: run.RunID, Selected: toolNode}, Field: "output"})
	if err != nil || !managed || preview == nil || !body.Available || !strings.Contains(strings.Join(preview.Texts, ""), "private owner body") {
		t.Fatal("native receipt interpretation missed plugin", preview, managed, err)
	}
	if !reflect.DeepEqual(before, counts()) {
		t.Fatal("read-only view wrote plugin state or audit")
	}
	first, err := f.host.Store.Graph(ctx, status.Revision)
	if err != nil {
		t.Fatal(err)
	}
	secondPackage, err := f.host.Install(ctx, tracePackageVersion(t, "1.1.0", false))
	if err != nil {
		t.Fatal(err)
	}
	updated := first.Graph
	updated.Bindings = append([]rsiplugin.Binding(nil), first.Graph.Bindings...)
	for i, b := range updated.Bindings {
		if b.InstanceID == "trace-inspector" {
			updated.Bindings[i].PackageDigest = secondPackage.Digest
		}
	}
	second, err := f.host.Prepare(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	unaccepted := query
	unaccepted.ReaderRevision = second.Revision
	if _, _, err := inspector.ProjectedObservations(ctx, unaccepted); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("unactivated display candidate received owner facts", err)
	}

	var policyID string
	if err = f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&policyID); err != nil {
		t.Fatal(err)
	}

	upgradedPolicy, err := f.host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	upgradedPolicy.ID = "trace-workbench-upgrade-owner"
	upgradedPolicy.AllowedContracts = append(upgradedPolicy.AllowedContracts, "steward.trace-workbench/1", "steward.trace-activity/1")
	resourceCasesRaw, err := os.ReadFile("builtin/trace-workbench-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var resourceCases []rsi.Case
	if err = json.Unmarshal(resourceCasesRaw, &resourceCases); err != nil {
		t.Fatal(err)
	}
	upgradedPolicy.Cases = append(upgradedPolicy.Cases, resourceCases...)
	for i := range upgradedPolicy.Cases {
		if upgradedPolicy.Cases[i].Contract == "steward.trace-layout/1" {
			upgradedPolicy.Cases[i].Expected = json.RawMessage(`{"nodes":[{"id":"root","depth":0,"parent_outside":false,"has_children":true,"guides":[],"x":0,"width":1000,"primary":true,"primary_has_children":true},{"id":"child","depth":1,"parent_outside":false,"has_children":false,"guides":[false],"x":200,"width":400,"primary":true}],"ttft_ms":2,"tabs":["overview","context","wire","diff"],"timeline":{"start":1000000,"end":2000000},"first_output_at":1002000}`)
		}
	}
	if err = f.host.Store.PutPolicy(ctx, upgradedPolicy); err != nil {
		t.Fatal(err)
	}
	f.host.Compute = driver.New(nil)
	selectVersion := func(id, baseline, target string) {
		t.Helper()
		selectedPolicy := policyID
		if target == second.Revision {
			selectedPolicy = upgradedPolicy.ID
		}
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: selectedPolicy, Goal: "independent trace view upgrade"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	selectVersion("trace-upgrade", first.Revision, second.Revision)
	currentSelection, err := inspector.TraceWorkbenchSelection(ctx, "")
	if err != nil || !currentSelection.Managed || currentSelection.ReaderRevision != second.Revision {
		t.Fatal("upgraded page did not select real workbench", currentSelection, err)
	}
	if legacySelection, err = inspector.TraceWorkbenchSelection(ctx, first.Revision); err != nil || legacySelection.Managed {
		t.Fatal("old page changed interpreter after upgrade", legacySelection, err)
	}
	pinnedQuery := query
	pinnedQuery.ReaderRevision = first.Revision
	if _, pinned, err := inspector.ProjectedObservations(ctx, pinnedQuery); err != nil || pinned == nil || pinned.ReaderRevision != first.Revision {
		t.Fatal("old owner interpretation changed during upgrade", pinned, err)
	}

	if _, view, err := inspector.ProjectedObservations(ctx, query); err != nil || view == nil {
		t.Fatal("old Trace URL failed after independent upgrade", view, err)
	}
	selectVersion("trace-rollback", second.Revision, first.Revision)
	if err = f.host.Store.Retire(ctx, secondPackage.Digest); err != nil {
		t.Fatal(err)
	}
	retiredQuery := query
	retiredQuery.ReaderRevision = second.Revision
	if _, pinned, err := inspector.ProjectedObservations(ctx, retiredQuery); err != nil || pinned == nil || pinned.ReaderRevision != second.Revision {
		t.Fatal("retired owner continuation lost package", pinned, err)
	}

	policy, err := f.host.Store.Policy(ctx, upgradedPolicy.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range policy.Cases {
		if test.InstanceID == "trace-inspector" {
			value, err := f.host.ReadService(ctx, second.Revision, test.Contract, test.Input, test.Inputs)
			if err != nil {
				t.Fatal("retired view version lost implementation", test.Contract, err)
			}
			got, _ := rsiplugin.Canonical(value)
			want, _ := rsiplugin.Canonical(test.Expected)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("retained view changed", test.Contract, string(value))
			}
		}
	}
	if _, err = f.host.Store.Deactivate(ctx, "disable-trace", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err = inspector.ProjectedObservations(ctx, query); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("disabled projection fell back to native", err)
	}
	if _, view, err := inspector.ProjectedObservations(ctx, pinnedQuery); err != nil || view == nil {
		t.Fatal("deactivation broke archived owner reference", view, err)
	}
	if err = f.host.Store.Quarantine(ctx, first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inspector.ProjectedObservations(ctx, pinnedQuery); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("quarantine did not revoke pinned owner interpretation", err)
	}

	// Raw diagnostics remain available to the independent Host control plane;
	// disabling a display policy does not delete or alter authoritative records.
	if raw, err := inspector.Observations(ctx, query); err != nil || len(raw.Items) != len(data.Items) {
		t.Fatal("source facts changed", raw, err)
	}
}

func TestTracePrivacyEpochRevokesBeforeInputRelease(t *testing.T) {
	requireSandbox(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newFixture(t)
	run, err := f.actor.Submit(ctx, "private-trace", "owner input")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: docxPackageSource(t), OCR: ocrPackageSource(t), Trace: tracePackageVersion(t, "1.1.0", true)}); err != nil {
		t.Fatal(err)
	}
	inspector := f.store.Inspector()
	inspector.ConfigureTracePlugins(f.host.Catalog)
	jobs := t.TempDir()
	t.Setenv("TMPDIR", jobs)
	type outcome struct {
		view *observability.TracePresentation
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		_, view, err := inspector.ProjectedObservations(ctx, observability.ObservationQuery{RunID: run.RunID})
		done <- outcome{view, err}
	}()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var marker string
wait:
	for {
		select {
		case err := <-done:
			t.Fatal("projection exited before revocation", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
			matches, _ := filepath.Glob(filepath.Join(jobs, "steward-rsi-*", "trace-ready"))
			if len(matches) == 1 {
				marker = matches[0]
				break wait
			}
		}
	}
	if _, err = f.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,updated_at) SELECT agent_id,1,1 FROM agent_config WHERE singleton=1 ON CONFLICT(agent_id) DO UPDATE SET epoch=epoch+1,updated_at=updated_at+1`); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(marker), "resume"), []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err == nil || result.view != nil {
			t.Fatal("privacy epoch ignored", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
