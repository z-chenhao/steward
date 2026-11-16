package rsi_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestTraceWorkbenchActualSealedProcessResourceAndReadOnlyLedger(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	p, err := f.host.Install(ctx, tracePackageVersion(t, "1.1.0", false))
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "trace-inspector", PackageDigest: p.Digest, Config: json.RawMessage(`{}`), StateNamespace: "trace-inspector", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../plugin/traceinspector/view/trace-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	style, err := os.ReadFile("../plugin/traceinspector/view/trace-activity.css")
	if err != nil {
		t.Fatal(err)
	}
	view, err := os.ReadFile("../plugin/traceinspector/view/trace.html")
	if err != nil {
		t.Fatal(err)
	}
	copySource, err := os.ReadFile("../plugin/traceinspector/view/copy.json")
	if err != nil {
		t.Fatal(err)
	}
	want := contract.WorkbenchResult{Copy: contract.Template{Resource: contract.CopyResource, Digest: rsiplugin.FileDigest(copySource), Source: string(copySource)}, Template: contract.Template{Resource: contract.TemplateResource, Digest: rsiplugin.FileDigest(view), Source: string(view)}, Program: contract.BrowserProgram{Resource: contract.ProgramResource, Digest: rsiplugin.FileDigest(source), Source: string(source)}, Stylesheet: contract.Stylesheet{Resource: contract.StylesheetResource, Digest: rsiplugin.FileDigest(style), Source: string(style)}}
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.host.Catalog.Load(p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, resource := range sealed.Manifest.Resources {
		if resource.Name == contract.ProgramResource {
			found = resource.Kind == "view" && resource.Path == contract.ProgramFile
		}
	}
	if !found {
		t.Fatal("program not a sealed package resource")
	}
	policy := rsi.Policy{ID: "trace-workbench-original-resource", AllowedContracts: []string{contract.Workbench, contract.Activity, contract.Contract, contract.DiffContract, contract.ToolContract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "trace-inspector", Contract: contract.Workbench, Input: json.RawMessage(`{}`), Expected: expected}}}

	for _, c := range []struct{ service, input, expected string }{
		{contract.Contract, `{"started":0,"now":1,"items":[],"timings":[]}`, `{"timeline":{"start":0,"end":1},"nodes":[],"tabs":["overview"]}`},
		{contract.DiffContract, `{"section":"messages","before":[],"after_records":[]}`, `{"items":[]}`},
		{contract.ToolContract, `{}`, `null`},
		{contract.Activity, `{"first":1000000,"last":1000000}`, `{"present":true,"start":1000000,"end":61000000,"base":0,"step":5000000}`},
	} {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "trace-inspector", Contract: c.service, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: []byte(c.input)}}, Expected: json.RawMessage(c.expected)})
	}
	if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := rsi.Candidate{ID: "trace-workbench-first-resource", Target: g.Revision, PolicyID: policy.ID, Goal: "Preserve independently captured Trace UI behavior"}
	if err = f.host.Propose(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	decision, err := f.host.Verify(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: candidate.ID, Target: g.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	counts := func() []int {
		t.Helper()
		result := make([]int, 3)
		for i, table := range []string{"rsi_call", "rsi_state", "rsi_audit"} {
			if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&result[i]); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := counts()
	inspector := f.store.Inspector()
	inspector.ConfigureTracePlugins(f.host.Catalog)
	selection, err := inspector.TraceWorkbenchSelection(ctx, "")
	if err != nil || !selection.Managed || selection.ReaderRevision != g.Revision {
		t.Fatal("current page failed to freeze accepted workbench", selection, err)
	}
	program, err := inspector.TraceWorkbenchProgram(ctx, g.Revision)
	if err != nil || program.ReaderRevision != g.Revision || program.Source != string(source) || program.Stylesheet != string(style) || program.ViewSource != string(view) || selection.ViewSource != string(view) {
		t.Fatal("actual inspector did not read sealed resource", err)
	}
	if _, err = inspector.TraceWorkbenchProgram(ctx, ""); err == nil {
		t.Fatal("resource read silently selected current graph")
	}
	result, err := f.host.ReadService(ctx, g.Revision, contract.Workbench, json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got contract.WorkbenchResult
	if err = rsiplugin.DecodeResult(result, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("actual process changed sealed workbench program")
	}
	if !reflect.DeepEqual(counts(), before) {
		t.Fatal("read-only program lookup wrote ledger")
	}
	if _, err = f.host.ReadService(ctx, g.Revision, contract.Workbench, json.RawMessage(`{"sql":"private"}`), nil); err == nil {
		t.Fatal("unknown authority accepted")
	}
	if _, err = f.host.ReadService(ctx, g.Revision, contract.Workbench, json.RawMessage(`{}`), []rsi.InputData{{ID: "private-body", Data: []byte("private")}}); err == nil {
		t.Fatal("unnecessary domain input accepted")
	}
	if _, err = f.host.Store.Deactivate(ctx, "trace-workbench-disabled", g.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.ReadService(ctx, g.Revision, contract.Workbench, json.RawMessage(`{}`), nil); err != nil {
		t.Fatal("retained exact graph lost its resource", err)
	}
	if _, err = inspector.TraceWorkbenchSelection(ctx, ""); err == nil {
		t.Fatal("deactivated current page fell back to native")
	}
	if selection, err = inspector.TraceWorkbenchSelection(ctx, g.Revision); err != nil || !selection.Managed {
		t.Fatal("explicit retained page lost interpreter", selection, err)
	}
	if program, err = inspector.TraceWorkbenchProgram(ctx, g.Revision); err != nil || program.Source != string(source) || program.Stylesheet != string(style) || program.ViewSource != string(view) || selection.ViewSource != string(view) {
		t.Fatal("deactivation lost explicitly frozen resource", err)
	}
}
