package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type terminalOwnerState struct{}

func (terminalOwnerState) Diagnostics(context.Context) (agent.Diagnostics, error) {
	return agent.Diagnostics{Readiness: "ready"}, nil
}
func (terminalOwnerState) History(context.Context) ([]conversation.Message, error) { return nil, nil }
func TestTerminalInterfaceActualUpgradeRetainedSessionRollbackStopAndCold(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), Terminal: defaultFeaturePackage(t, "terminal-interface", nil)}); err != nil {
		t.Fatal(err)
	}
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reader := func() func(context.Context, contract.Query) (contract.Result, bool, error) {
		return a.terminalInterfaceReader(func() rsi.Host { return host })
	}
	view := func(read func(context.Context, contract.Query) (contract.Result, bool, error), variant bool) {
		t.Helper()
		out, managed, e := read(ctx, contract.Query{Operation: "view"})
		if e != nil || !managed || out.View == nil || strings.Contains(out.View.Help, "[terminal v2]") != variant {
			t.Fatal("wrong actual terminal owner", out, e)
		}
	}
	run := func(read func(context.Context, contract.Query) (contract.Result, bool, error), variant bool) {
		t.Helper()
		s := a.Terminal()
		s.Interface = read
		s.State = terminalOwnerState{}
		s.Messages = nil
		var output bytes.Buffer
		if err := s.Run(ctx, io.NopCloser(strings.NewReader("/status\n/quit\n")), &output); err != nil || !strings.Contains(output.String(), "State: ready") || strings.Contains(output.String(), "[terminal v2]") != variant {
			t.Fatal("actual terminal did not consume owner", output.String(), err)
		}
	}
	choice := func(read func(context.Context, contract.Query) (contract.Result, bool, error), want int) {
		t.Helper()
		f := contract.ChoiceFacts{Count: 2, Exact: 1, Number: 0, Manual: true}
		out, managed, e := read(ctx, contract.Query{Operation: "choose-model", Facts: &f})
		if e != nil || !managed || out.Choice == nil || out.Choice.Kind != "index" || out.Choice.Index != want {
			t.Fatal("actual selection method not used", out, e)
		}
	}
	old := reader()
	view(old, false)
	run(old, false)
	choice(old, 1)
	raw, err := os.ReadFile("../plugin/terminalinterface/project.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	text = strings.Replace(text, `Version: "1.0.0"`, `Version: "1.1.0"`, 1)
	text = strings.Replace(text, "Steward — /setup", "Steward [terminal v2] — /setup", 1)
	exact := `if f.Exact >= 0 {
		return contract.Choice{Kind: "index", Index: f.Exact}
	}
	if f.Number >= 0 {
		return contract.Choice{Kind: "index", Index: f.Number}
	}`
	numeric := `if f.Number >= 0 {
		return contract.Choice{Kind: "index", Index: f.Number}
	}
	if f.Exact >= 0 {
		return contract.Choice{Kind: "index", Index: f.Exact}
	}`
	if !strings.Contains(text, exact) {
		t.Fatal("original choice ordering absent")
	}
	text = strings.Replace(text, exact, numeric, 1)
	replacement := filepath.Join(t.TempDir(), "project.go")
	if err = os.WriteFile(replacement, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	source, _ := filepath.Abs("../plugin/terminalinterface/project.go")
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {source: replacement}})
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayFile, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if out, e := exec.Command("go", "build", "-overlay", overlayFile, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/terminal-interface").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	raw, err = os.ReadFile("../../plugins/terminal-interface/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := host.Install(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "terminal-interface" {
			graph.Graph.Bindings[i].PackageDigest = installed.Digest
		}
	}
	second, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	var oldPolicy string
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&oldPolicy); err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, oldPolicy)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "terminal-independent-v2"
	// Owner declares the intended two changes; candidate output is never learned.
	for i := range policy.Cases {
		if policy.Cases[i].Contract != contract.Service {
			continue
		}
		var q contract.Query
		var expected contract.Result
		if err = json.Unmarshal(policy.Cases[i].Input, &q); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(policy.Cases[i].Expected, &expected); err != nil {
			t.Fatal(err)
		}
		if expected.View != nil {
			expected.View.Help = strings.Replace(expected.View.Help, "Steward — /setup", "Steward [terminal v2] — /setup", 1)
		}
		if q.Facts != nil && q.Facts.Exact >= 0 && q.Facts.Number >= 0 {
			expected.Choice.Index = q.Facts.Number
		}
		policy.Cases[i].Expected, _ = json.Marshal(expected)
	}
	if err = host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activate := func(id, from, to, policyID string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policyID, Goal: "independent terminal presentation and choice"}); e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: from, Target: to, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	activate("terminal-upgrade", first.Revision, second.Revision, policy.ID)
	current := reader()
	view(current, true)
	run(current, true)
	choice(current, 0)
	view(old, false)
	choice(old, 1)
	activate("terminal-rollback", second.Revision, first.Revision, oldPolicy)
	if err = host.Store.Retire(ctx, installed.Digest); err != nil {
		t.Fatal(err)
	}
	run(current, true)
	choice(current, 0)
	run(reader(), false)
	choice(reader(), 1)
	if err = host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, _, e := current(ctx, contract.Query{Operation: "view"}); e == nil {
		t.Fatal("Guardian stop ignored by frozen reader")
	}
	if err = host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	choice(current, 0)
	if _, err = host.Store.Deactivate(ctx, "terminal-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, e := reader()(ctx, contract.Query{Operation: "view"}); e == nil {
		t.Fatal("disabled terminal restored native")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	if _, _, e := reader()(ctx, contract.Query{Operation: "view"}); e == nil {
		t.Fatal("cold terminal restored native")
	}
	var calls int
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT count(*) FROM rsi_call`).Scan(&calls); err != nil || calls != 0 {
		t.Fatal("pure terminal UI wrote private call/effect receipts", calls, err)
	}
}
