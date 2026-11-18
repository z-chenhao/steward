package rsi_test

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"

	"reflect"
	"testing"
)

func memoryInspectorPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, e := exec.Command("python3", "../../tests/fixtures/plugins/memory-inspector-original-v1/build.py", dir).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	return dir
}
func TestMemoryInspectorActualIsolatedPresentationMatchesOriginalWithoutLedgerWrites(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	p, e := f.host.Install(ctx, memoryInspectorPackage(t))
	if e != nil {
		t.Fatal(e)
	}
	g, e := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "memory-inspector", PackageDigest: p.Digest, Config: json.RawMessage(`{}`), StateNamespace: "memory-inspector", StateVersion: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/behavior.json")
	if e != nil {
		t.Fatal(e)
	}
	var original struct {
		URLs []struct {
			Query    contract.Query `json:"query"`
			Key      string         `json:"key"`
			Value    string         `json:"value"`
			Expected string         `json:"expected"`
		} `json:"urls"`
	}
	if e = json.Unmarshal(raw, &original); e != nil {
		t.Fatal(e)
	}
	req := contract.Request{Navigation: make([]contract.Navigation, len(original.URLs))}
	want := contract.Result{URLs: make([]string, len(original.URLs)), Labels: contract.Labels{Source: contract.SourceLabels{Conversation: "Source message", Task: "Source task", Tool: "memory_manage", Control: "Local control", Other: "Source"}, Origin: contract.OriginLabels{Written: "Written note", Dream: "Dreaming", Original: "Original record", Automatic: "Automatically extracted", Explicit: "Explicitly remembered", Other: "Unknown origin"}, Layer: contract.LayerLabels{Transcripts: "Transcripts", Representations: "Memory representations", Injected: "Injected context", Notes: "Memory notes"}, DefaultStatus: "active"}}
	for i, c := range original.URLs {
		req.Navigation[i] = contract.Navigation{Query: c.Query, Key: c.Key, Value: c.Value}
		want.URLs[i] = c.Expected
	}
	capturedView, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/managed-memory-v1.html.txt")
	if err != nil {
		t.Fatal(err)
	}
	want.View = contract.View{Resource: contract.ViewResource, Digest: rsiplugin.FileDigest(capturedView), Source: string(capturedView)}
	styles, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/memory.css.txt")
	if err != nil {
		t.Fatal(err)
	}
	want.Stylesheet = contract.Stylesheet{Resource: contract.StylesheetResource, Digest: rsiplugin.FileDigest(styles), Source: string(styles)}
	copy, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	want.Localization = contract.Localization{Resource: contract.LocalizationResource, Digest: rsiplugin.FileDigest(copy), Source: string(copy)}
	program, err := os.ReadFile("../../tests/fixtures/plugins/memory-inspector-original-v1/worker-v1.js.txt")
	if err != nil {
		t.Fatal(err)
	}
	want.Program = contract.BrowserProgram{Resource: contract.ProgramResource, Digest: rsiplugin.FileDigest(program), Source: string(program)}
	input, _ := json.Marshal(req)
	expected, _ := json.Marshal(want)
	policy := rsi.Policy{ID: "original-memory-inspector", AllowedContracts: []string{contract.Presentation}, Cases: []rsi.Case{{InstanceID: "memory-inspector", Contract: contract.Presentation, Input: input, Expected: expected}}}
	if e = f.host.Store.PutPolicy(ctx, policy); e != nil {
		t.Fatal(e)
	}
	candidate := rsi.Candidate{ID: "first-memory-inspector", Target: g.Revision, PolicyID: policy.ID, Goal: "Preserve original owner-only memory presentation"}
	if e = f.host.Propose(ctx, candidate); e != nil {
		t.Fatal(e)
	}
	decision, e := f.host.Verify(ctx, candidate.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.host.Activate(ctx, rsi.Activation{OperationID: candidate.ID, Target: g.Revision, DecisionID: decision}); e != nil {
		t.Fatal(e)
	}
	counts := func() []int {
		v := make([]int, 3)
		for i, table := range []string{"rsi_call", "rsi_state", "rsi_audit"} {
			if e := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&v[i]); e != nil {
				t.Fatal(e)
			}
		}
		return v
	}
	before := counts()
	result, e := f.host.ReadService(ctx, g.Revision, contract.Presentation, input, nil)
	if e != nil {
		t.Fatal(e)
	}
	var got contract.Result
	if e = rsiplugin.DecodeResult(result, &got); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("actual producer changed original navigation/labels", got, want)
	}
	if !reflect.DeepEqual(before, counts()) {
		t.Fatal("pure owner read wrote ledger")
	}
	if _, e = f.host.ReadService(ctx, g.Revision, contract.Presentation, input, []rsi.InputData{{ID: "forbidden-domain-page", Data: []byte("private page")}}); e == nil {
		t.Fatal("undeclared domain input accepted")
	}
	if _, e = f.host.ReadService(ctx, g.Revision, contract.Presentation, json.RawMessage(`{"navigation":[],"sql":"select private"}`), nil); e == nil {
		t.Fatal("unknown authority accepted")
	}
	if _, e = f.host.Store.Deactivate(ctx, "disable-memory-inspector", g.Revision); e != nil {
		t.Fatal(e)
	}
	// Explicit retained graph reads remain possible; current selection semantics are
	// proved by the real Web adapter once it is wired, not inferred from this test.
	if _, e = f.host.ReadService(ctx, g.Revision, contract.Presentation, input, nil); e != nil {
		t.Fatal("deactivation lost retained graph", e)
	}
}
