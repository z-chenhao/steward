package rsi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/sys/unix"
)

var fixtureExecutable string

func TestMain(m *testing.M) {
	if handled, err := parserresource.Exec(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if handled, err := rsi.ExecPlugin(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if filepath.Base(os.Args[0]) == "plugin" {
		runFixture()
		os.Exit(0)
	}
	fixtureExecutable, _ = os.Executable()
	cleanup := func() {}
	info, err := os.Stat(fixtureExecutable)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The candidate must fit the same 32 MiB package boundary as production.
	// Linux debug symbols can push this shared test carrier beyond that limit.
	if raceBuild || info.Size() > 32<<20 {
		root, err := os.MkdirTemp("", "steward-rsi-carrier-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cleanup = func() { os.RemoveAll(root) }
		fixtureExecutable = filepath.Join(root, "carrier")
		cmd := exec.Command("go", "test", "-c", "-race=false", "-ldflags=-s -w", "-o", fixtureExecutable, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, string(out))
			cleanup()
			os.Exit(1)
		}
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
func runFixture() {
	exe, _ := os.Executable()
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "steward.plugin.json"))
	var manifest rsiplugin.Manifest
	_ = rsiplugin.Decode(raw, &manifest)
	if manifest.Version == "99.0.0" {
		fmt.Fprintln(os.Stderr, "PRIVATE_CANDIDATE_STDERR_NOT_DIAGNOSTIC")
		os.Exit(2)
	}
	var config struct {
		Tag string `json:"tag"`
	}
	h := rsiplugin.Handlers{PluginID: manifest.PluginID, Version: manifest.Version, OnPrepare: func(_ context.Context, p rsiplugin.PrepareInput) error {
		if err := rsiplugin.Decode(p.Config, &config); err != nil {
			return err
		}
		if config.Tag == "prepare-fail" {
			return errors.New("fixture preparation rejected")
		}
		if config.Tag == "prepare-delay" {
			time.Sleep(700 * time.Millisecond)
		}
		return nil
	}, Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){}, Hooks: map[rsiplugin.Stage]func(context.Context, rsiplugin.HookCall) (rsiplugin.HookResult, error){}}
	verificationCalls := 0
	for _, s := range manifest.Provides {
		h.Services[s.Contract] = func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var in struct {
				Text    string `json:"text"`
				Path    string `json:"path"`
				Address string `json:"address"`
			}
			if err := rsiplugin.Decode(c.Input, &in); err != nil {
				return rsiplugin.Result{}, err
			}
			if in.Text == "allocator-environment" {
				return rsiplugin.JSONResult(ctx, map[string]string{"text": os.Getenv("MALLOC_ARENA_MAX")})
			}
			if in.Text == "kernel-limits" {
				var limits testProcessLimits
				for _, spec := range []struct {
					resource   int
					soft, hard *uint64
				}{
					{unix.RLIMIT_CPU, &limits.CPUSoft, &limits.CPUHard},
					{unix.RLIMIT_FSIZE, &limits.FileSoft, &limits.FileHard},
					{unix.RLIMIT_NOFILE, &limits.FDSoft, &limits.FDHard},
					{unix.RLIMIT_AS, &limits.ASSoft, &limits.ASHard},
				} {
					var value unix.Rlimit
					if err := unix.Getrlimit(spec.resource, &value); err != nil {
						return rsiplugin.Result{}, err
					}
					*spec.soft, *spec.hard = value.Cur, value.Max
				}
				limits.RaiseDenied = unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: limits.FDHard + 1, Max: limits.FDHard + 1}) != nil
				return rsiplugin.JSONResult(ctx, limits)
			}
			if in.Text == "verification-delay" {
				verificationCalls++
				time.Sleep(700 * time.Millisecond)
				return rsiplugin.JSONResult(ctx, map[string]string{"text": fmt.Sprint(verificationCalls)})
			}
			if strings.HasPrefix(in.Text, "resource") {
				text := in.Path
				if in.Text == "resource-large" {
					text = strings.Repeat("合同<>🧭", 2100000)
				}
				if in.Text == "resource-budget" {
					time.Sleep(450 * time.Millisecond)
				}
				var value struct {
					Text string `json:"text"`
				}
				contract := "steward.fixture-resource/1"
				if in.Text == "resource-unknown" {
					contract = "steward.private-resource/1"
				}
				if err := rsiplugin.CallResource(ctx, contract, "original", map[string]string{"text": text}, &value); err != nil {
					return rsiplugin.Result{}, err
				}
				if in.Text == "resource-budget" {
					time.Sleep(450 * time.Millisecond)
				}
				if in.Text == "resource-repeat" || in.Text == "resource-conflict" {
					if in.Text == "resource-conflict" {
						text += "changed"
					}
					if err := rsiplugin.CallResource(ctx, contract, "original", map[string]string{"text": text}, &value); err != nil {
						return rsiplugin.Result{}, err
					}
				}
				return rsiplugin.JSONResult(ctx, map[string]string{"text": fmt.Sprint(len(value.Text))})
			}
			if in.Text == "timeout" {
				select {}
			}
			if in.Text == "dependency" {
				var value map[string]string
				if err := rsiplugin.CallDependency(ctx, "upstream", "one", map[string]string{"text": "nested"}, &value); err != nil {
					return rsiplugin.Result{}, err
				}
				raw, _ := json.Marshal(value)
				return rsiplugin.Result{Output: raw}, nil
			}
			if in.Text == "propose" {
				receipt, err := rsiplugin.Propose(ctx, rsiplugin.Proposal{Target: in.Path, PolicyID: in.Address, Goal: "fixture proposal"})
				if err != nil {
					return rsiplugin.Result{}, err
				}
				raw, _ := json.Marshal(map[string]string{"text": receipt.CandidateID})
				return rsiplugin.Result{Output: raw}, nil
			}
			if in.Text == "security" {
				_, read := os.ReadFile(in.Path)
				conn, e := net.DialTimeout("tcp", in.Address, 100*time.Millisecond)
				if conn != nil {
					conn.Close()
				}
				fork := exec.Command("/bin/true").Run()
				raw, _ := json.Marshal(map[string]bool{"read": read == nil, "network": e == nil, "fork": fork == nil})
				return rsiplugin.Result{Output: raw}, nil
			}
			if in.Text == "count" {
				var state struct {
					Count int `json:"count"`
				}
				_ = json.Unmarshal(c.State.Value, &state)
				state.Count++
				value, _ := json.Marshal(state)
				change := json.RawMessage(value)
				return rsiplugin.Result{Output: json.RawMessage(fmt.Sprintf(`{"text":"%d"}`, state.Count)), State: &change}, nil
			}
			value, _ := json.Marshal(map[string]string{"text": strings.TrimSpace(in.Text) + config.Tag})
			return rsiplugin.Result{Output: value}, nil
		}
	}
	for _, hook := range manifest.Hooks {
		h.Hooks[hook.Stage] = func(_ context.Context, c rsiplugin.HookCall) (rsiplugin.HookResult, error) {
			out := rsiplugin.HookResult{}
			switch c.Input.Stage {
			case rsiplugin.BeforeModelRequest:
				out.Output.AdditionalContext = []rsiplugin.Message{{Kind: "plugin_context", Text: "note:" + config.Tag}}
			case rsiplugin.BeforeToolCall:
				out.Output.Arguments = json.RawMessage(`{"text":"changed"}`)
			case rsiplugin.PrepareReply:
				text := "formatted:" + c.Input.Text
				out.Output.Text = &text
			case rsiplugin.SelectCapabilities:
				names := []string{"plugin__echo"}
				if config.Tag == "drop" {
					names = []string{}
				}
				out.Output.ToolNames = &names
			}
			return out, nil
		}
	}
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, h); err != nil {
		os.Exit(2)
	}
}

type probeFixture struct{}

func (probeFixture) Run(context.Context, llm.Profile) (llm.ProbeResult, error) {
	return llm.ProbeResult{Streaming: true, ToolCalls: true, Continuation: true, Cancellation: true, Clipping: true}, nil
}

type fixture struct {
	host  rsi.Host
	store *agentsqlite.Store
	runs  *agentsqlite.Runs
	actor agent.Actor
	root  string
	db    *sql.DB
}
type recordingProvider struct {
	store   *agentsqlite.Store
	respond func(llm.GenerationRequest) llm.GenerationResult
}

func (p recordingProvider) Generate(ctx context.Context, req llm.GenerationRequest, _ func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	out := llm.GenerationResult{Text: "done"}
	if p.respond != nil {
		out = p.respond(req)
	}
	err := p.store.Models().RecordUsageAnd(ctx, req, out, time.Millisecond, nil, p.store.AcceptGeneration)
	return out, err
}
func (p recordingProvider) Continue(ctx context.Context, req llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.Generate(ctx, req, sink)
}
func (recordingProvider) Cancel(context.Context, string) error  { return nil }
func (recordingProvider) Dispose(context.Context, string) error { return nil }
func newFixture(t *testing.T, more ...tool.Definition) *fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	s, err := agentsqlite.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	history, err := conversationsqlite.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.Close() })
	registry := tool.FixtureRegistry()
	if len(more) > 0 {
		defs := append([]tool.Definition{}, more...)
		for _, spec := range registry.Describe() {
			definition, _ := registry.Resolve(spec.Name)
			defs = append(defs, definition)
		}
		registry, err = tool.NewRegistry(defs)
		if err != nil {
			t.Fatal(err)
		}
	}
	runs := s.Runs(history, registry)
	catalog := rsi.NewCatalog(root)
	catalog.Launcher = fixtureExecutable
	runs.ConfigurePlugins(catalog)
	profile, err := s.Models().AddProfile(ctx, llm.ProfileInput{Name: "test", ProviderKind: "openai_compatible", Endpoint: "http://127.0.0.1:8000/v1", WireAPI: "openai_completions", ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 1024, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true}})
	if err != nil {
		t.Fatal(err)
	}
	control := llm.Control{Store: s.Models(), Probe: probeFixture{}, BridgeIdentity: "test:rsi"}
	if _, err = control.Test(ctx, profile.ProfileID); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Diagnostics(ctx)
	if err = control.Select(ctx, profile.ProfileID, profile.Version, d.ConfigVersion); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &fixture{host: rsi.Host{Catalog: catalog, Store: s.Plugins()}, store: s, runs: runs, actor: agent.Actor{Store: runs, Registry: registry, Provider: recordingProvider{store: s}, Ready: func(context.Context) bool { return true }}, root: root, db: db}
}
func manifest() rsiplugin.Manifest {
	return rsiplugin.Manifest{Protocol: rsiplugin.Protocol, PluginID: "steward.test", Version: "1.0.0", Runtime: rsiplugin.Runtime{Entry: "plugin", TimeoutMS: 5000}, Provides: []rsiplugin.Service{{Contract: "steward.echo/1", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"path":{"type":"string"},"address":{"type":"string"}},"required":["text"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"tag":{"type":"string"}},"required":["tag"],"additionalProperties":false}`), State: rsiplugin.StateSpec{Version: 1, Schema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`), Initial: json.RawMessage(`{"count":0}`)}, Hooks: []rsiplugin.Hook{{Stage: rsiplugin.BeforeModelRequest}}, Tools: []rsiplugin.Tool{{Name: "plugin__echo", Description: "echo", Contract: "steward.echo/1"}}}
}
func install(t *testing.T, f *fixture, m rsiplugin.Manifest) rsi.Package {
	t.Helper()
	source := t.TempDir()
	data, err := os.ReadFile(fixtureExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "plugin"), data, 0700); err != nil {
		t.Fatal(err)
	}
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := f.host.Install(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func graph(t *testing.T, f *fixture, p rsi.Package, tag string) rsi.ResolvedGraph {
	t.Helper()
	config, _ := json.Marshal(map[string]string{"tag": tag})
	g := rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "test", PackageDigest: p.Digest, Config: config, StateNamespace: "test", StateVersion: 1}}}
	v, err := f.host.Prepare(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func decisionFor(t *testing.T, f *fixture, g rsi.ResolvedGraph, label string) rsi.Activation {
	t.Helper()
	ctx := context.Background()
	p := rsi.Policy{ID: "policy-" + label, AllowedContracts: []string{"steward.echo/1"}}
	for _, b := range g.Graph.Bindings {
		var config struct {
			Tag string `json:"tag"`
		}
		_ = json.Unmarshal(b.Config, &config)
		pkg, err := f.host.Catalog.Load(b.PackageDigest)
		if err != nil {
			t.Fatal(err)
		}
		p.AllowedRequests = append(p.AllowedRequests, pkg.Manifest.Requests...)
		p.Cases = append(p.Cases, rsi.Case{InstanceID: b.InstanceID, Contract: "steward.echo/1", Input: json.RawMessage(`{"text":" check "}`), Expected: json.RawMessage(fmt.Sprintf(`{"text":%q}`, "check"+config.Tag))})
		for _, hook := range pkg.Manifest.Hooks {
			input := rsiplugin.HookInput{Stage: hook.Stage}
			expected := json.RawMessage(`{}`)
			switch hook.Stage {
			case rsiplugin.BeforeModelRequest:
				expected = json.RawMessage(fmt.Sprintf(`{"additional_context":[{"kind":"plugin_context","text":%q}]}`, "note:"+config.Tag))
			case rsiplugin.BeforeToolCall:
				input.ToolCall = &rsiplugin.ToolCall{ID: "call", Name: "plugin__echo", Arguments: json.RawMessage(`{"text":"original"}`)}
				expected = json.RawMessage(`{"arguments":{"text":"changed"}}`)
			case rsiplugin.PrepareReply:
				input.Text = "reply"
				expected = json.RawMessage(`{"text":"formatted:reply"}`)
			case rsiplugin.SelectCapabilities:
				input.ToolNames = []string{"plugin__echo"}
				expected = json.RawMessage(`{"tool_names":["plugin__echo"]}`)
				if config.Tag == "drop" {
					expected = json.RawMessage(`{"tool_names":[]}`)
				}
			}
			p.AllowedHooks = append(p.AllowedHooks, hook.Stage)
			p.Cases = append(p.Cases, rsi.Case{InstanceID: b.InstanceID, Hook: &input, Expected: expected})
		}
	}
	if err := f.host.Store.PutPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	status, _ := f.host.Store.Status(ctx)
	candidate := rsi.Candidate{ID: "candidate-" + label, Baseline: status.Revision, Target: g.Revision, PolicyID: p.ID, Goal: "verify fixture"}
	if err := f.host.Propose(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	decision, err := f.host.Verify(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rsi.Activation{OperationID: "activate-" + label, Expected: status.Revision, Target: g.Revision, DecisionID: decision}
}
func activate(t *testing.T, f *fixture, g rsi.ResolvedGraph, label string) rsi.Receipt {
	t.Helper()
	receipt, err := f.host.Activate(context.Background(), decisionFor(t, f, g, label))
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
func requireSandbox(t *testing.T) {
	t.Helper()
	if err := sandbox.Check(); err != nil {
		t.Skip("kernel sandbox unavailable:", err)
	}
}

func TestPackageVersionRejectsChangedContentAndRetainsOldRun(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	old := install(t, f, manifest())
	original := graph(t, f, old, "-original")
	activate(t, f, original, "original-version")
	run, err := f.actor.Submit(ctx, "versioned", "keep original package")
	if err != nil {
		t.Fatal(err)
	}

	// A rebuilt payload is new content even when its service contract is unchanged.
	source := t.TempDir()
	entry, err := os.ReadFile(fixtureExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "plugin"), entry, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "recipe.txt"), []byte("revised implementation resource"), 0600); err != nil {
		t.Fatal(err)
	}
	seal := func(m rsiplugin.Manifest) {
		t.Helper()
		sealed, e := rsi.Seal(source, m)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(sealed)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m := manifest()
	seal(m)
	if _, err = f.host.Install(ctx, source); !errors.Is(err, rsi.ErrConflict) {
		t.Fatalf("same version accepted changed content: %v", err)
	}
	if status, e := f.host.Store.Status(ctx); e != nil || status.Revision != original.Revision {
		t.Fatalf("conflict changed selected graph: %+v %v", status, e)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_package WHERE plugin_id=?`, m.PluginID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("conflict changed installed versions: %d %v", count, err)
	}
	m.Version = "1.0.1"
	seal(m)
	updated, err := f.host.Install(ctx, source)
	if err != nil || updated.Digest == old.Digest {
		t.Fatalf("patch version install: %s %v", updated.Digest, err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_package WHERE plugin_id=?`, m.PluginID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("old version not retained: %d %v", count, err)
	}
	if retained, e := f.host.Catalog.Load(old.Digest); e != nil || retained.Digest != old.Digest {
		t.Fatalf("original package changed: %v", e)
	}
	current := graph(t, f, updated, "-patch")
	activate(t, f, current, "patch-version")
	out, err := f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: run.RunID}})
	if err != nil || len(out.AdditionalContext) != 1 || out.AdditionalContext[0].Text != "note:-original" {
		t.Fatalf("patch changed frozen Run: %+v %v", out, err)
	}
}

func TestPackageGraphAndIsolation(t *testing.T) {
	requireSandbox(t)
	f := newFixture(t)
	p := install(t, f, manifest())
	g := graph(t, f, p, "-v1")
	b := g.Graph.Bindings[0]
	secret := filepath.Join(t.TempDir(), "private")
	_ = os.WriteFile(secret, []byte("PRIVATE_OUTSIDE_SANDBOX"), 0600)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	input, _ := json.Marshal(map[string]string{"text": "security", "path": secret, "address": listener.Addr().String()})
	var out rsiplugin.Result
	if err = (rsi.Runner{Catalog: f.host.Catalog}).Invoke(context.Background(), b, "call", rsiplugin.Call{Contract: "steward.echo/1", Input: input, State: rsiplugin.State{Value: p.Manifest.State.Initial}}, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Output) != `{"fork":false,"network":false,"read":false}` {
		t.Fatal("isolation failed", string(out.Output))
	}
	file := filepath.Join(f.host.Catalog.Path(p.Digest), "plugin")
	if err = os.Chmod(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("drift"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Catalog.Load(p.Digest); err == nil {
		t.Fatal("changed bytes accepted")
	}
}
func TestActivationPinsOldRunsAndAuditsAtomically(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	p := install(t, f, manifest())
	first := graph(t, f, p, "-v1")
	receipt := activate(t, f, first, "one")
	run, err := f.actor.Submit(ctx, "old", "old input")
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: run.RunID}})
	if err != nil || out.AdditionalContext[0].Text != "note:-v1" {
		t.Fatal(out, err)
	}
	second := graph(t, f, p, "-v2")
	activate(t, f, second, "two")
	same, err := f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: run.RunID}})
	if err != nil || same.AdditionalContext[0].Text != "note:-v1" {
		t.Fatal("old run drift", same, err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := f.actor.Submit(ctx, "next", "next input")
	if err != nil {
		t.Fatal(err)
	}
	nextOut, err := f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: next.RunID}})
	if err != nil || nextOut.AdditionalContext[0].Text != "note:-v2" {
		t.Fatal(nextOut, err)
	}
	if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: "activate-one", Target: first.Revision, DecisionID: "wrong"}); !errors.Is(err, rsi.ErrConflict) {
		t.Fatal("operation identity reused", err)
	}
	third := graph(t, f, p, "-v3")
	thirdActivation := decisionFor(t, f, third, "third")
	if _, err = f.db.Exec(`CREATE TRIGGER fail_rsi_audit BEFORE INSERT ON rsi_audit WHEN new.kind='graph.activated' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Activate(ctx, thirdActivation); err == nil {
		t.Fatal("activation ignored audit failure")
	}
	status, _ := f.host.Store.Status(ctx)
	if status.Epoch != receipt.Epoch+1 || status.Revision != second.Revision {
		t.Fatal(status)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_activation WHERE operation_id=?`, thirdActivation.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial activation receipt", count, err)
	}
	if err = f.host.Store.Quarantine(ctx, first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: run.RunID}}); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("quarantine did not fence old run", err)
	}
	audit, err := f.host.Store.Audit(ctx)
	if err != nil || len(audit) == 0 {
		t.Fatal(audit, err)
	}
}
func TestStateJournalRejectsUnknownReplayAndSeparatesRuns(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Requests = []string{"state.run.write"}
	p := install(t, f, m)
	g := graph(t, f, p, "")
	activate(t, f, g, "state")
	a, err := f.actor.Submit(ctx, "a", "one")
	if err != nil {
		t.Fatal(err)
	}
	in := json.RawMessage(`{"text":"count"}`)
	for i := 0; i < 2; i++ {
		v, err := f.host.Call(ctx, rsiplugin.Execution{RunID: a.RunID}, "test", "steward.echo/1", "call-one", in)
		if err != nil || string(v) != `{"text":"1"}` {
			t.Fatal(string(v), err)
		}
	}
	if _, err = f.host.Call(ctx, rsiplugin.Execution{RunID: a.RunID}, "test", "steward.echo/1", "call-one", json.RawMessage(`{"text":"changed"}`)); !errors.Is(err, rsi.ErrConflict) {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := f.actor.Submit(ctx, "b", "two")
	if err != nil {
		t.Fatal(err)
	}
	if b.RunID == a.RunID {
		t.Fatal("fixture reused run")
	}
	value, err := f.host.Call(ctx, rsiplugin.Execution{RunID: b.RunID}, "test", "steward.echo/1", "call-one", in)
	if err != nil || string(value) != `{"text":"1"}` {
		t.Fatal("state leaked across runs", string(value), err)
	}
	if _, _, err = f.host.Store.BeginCall(ctx, a.RunID, "test", "interrupted", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.host.Store.BeginCall(ctx, a.RunID, "test", "interrupted", "hash"); !errors.Is(err, rsi.ErrUnknown) {
		t.Fatal("unknown operation replayed", err)
	}
	if err = f.host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Call(ctx, rsiplugin.Execution{RunID: a.RunID}, "test", "steward.echo/1", "other", in); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal(err)
	}
}
func TestGraphRejectsMissingDependenciesAndDuplicateInstances(t *testing.T) {
	f := newFixture(t)
	p := install(t, f, manifest())
	b := rsiplugin.Binding{InstanceID: "test", PackageDigest: p.Digest, Config: json.RawMessage(`{"tag":"x"}`), StateNamespace: "test", StateVersion: 1}
	for _, g := range []rsiplugin.Graph{{Protocol: "other", Bindings: []rsiplugin.Binding{b}}, {Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{b, b}}} {
		if _, err := f.host.Catalog.Resolve(g); err == nil {
			t.Fatal("invalid graph accepted")
		}
	}
	m := manifest()
	m.Version = "2.0.0"
	m.Requires = []rsiplugin.Requirement{{Binding: "upstream", Contract: "steward.echo/1"}}
	p = install(t, f, m)
	b.PackageDigest = p.Digest
	if _, err := f.host.Catalog.Resolve(rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{b}}); err == nil {
		t.Fatal("missing dependency accepted")
	}
}

func TestLoopAdmitsTransformedToolArgumentsAndProjectsReply(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Hooks = []rsiplugin.Hook{{Stage: rsiplugin.BeforeToolCall}, {Stage: rsiplugin.PrepareReply}, {Stage: rsiplugin.RunSettled}}
	m.Requests = []string{"run.tool.read", "run.reply.read"}
	p := install(t, f, m)
	g := graph(t, f, p, "-service")
	activate(t, f, g, "loop")
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		for _, message := range req.Messages {
			if message.Role == "tool" && message.ToolCallID == "plugin-call" {
				return llm.GenerationResult{Text: "observed:" + message.Text}
			}
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "plugin-call", Name: "plugin__echo", Arguments: `{"text":"original"}`}}}
	}}
	receipt, err := f.actor.Submit(ctx, "tool-run", "run plugin")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var args string
	if err = f.db.QueryRow(`SELECT arguments_json FROM tool_invocation WHERE run_id=? AND tool_name='plugin__echo'`, receipt.RunID).Scan(&args); err != nil || args != `{"text":"changed"}` {
		t.Fatal("suggestion bypassed/lost at admission", args, err)
	}
	var original string
	if err = f.db.QueryRow(`SELECT json_extract(result_json,'$.tool_calls[0].arguments') FROM model_generation WHERE run_id=? AND step_index=0`, receipt.RunID).Scan(&original); err != nil || original != `{"text":"original"}` {
		t.Fatal("raw provider result modified", original, err)
	}
	history, err := conversationsqlite.Open(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	d, _ := f.store.Diagnostics(ctx)
	messages, err := history.List(ctx, d.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(messages)
	if !strings.Contains(string(raw), "formatted:observed:") || !strings.Contains(string(raw), "changed-service") {
		t.Fatal("reply projection missing", string(raw))
	}
	processed, err := f.store.Plugins().Work(ctx, f.host.Catalog)
	if err != nil || processed != 1 {
		t.Fatal("durable settled hook", processed, err)
	}
	processed, err = f.store.Plugins().Work(ctx, f.host.Catalog)
	if err != nil || processed != 0 {
		t.Fatal("event redelivered", processed, err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var generations int
	if err = f.db.QueryRow(`SELECT count(*) FROM model_generation WHERE run_id=?`, receipt.RunID).Scan(&generations); err != nil || generations != 2 {
		t.Fatal("completed work repeated", generations, err)
	}
}

func TestDeactivateRetainsOldBindingsAndUninstallBlocksNewSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	p := install(t, f, manifest())
	g := graph(t, f, p, "-old")
	activate(t, f, g, "lifecycle")
	run, err := f.actor.Submit(ctx, "old", "old")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.host.Store.Retire(ctx, p.Digest); !errors.Is(err, rsi.ErrConflict) {
		t.Fatal("retired active package", err)
	}
	if _, err = f.host.Store.Deactivate(ctx, "disable", g.Revision); err != nil {
		t.Fatal(err)
	}
	if err = f.host.Store.Retire(ctx, p.Digest); err != nil {
		t.Fatal(err)
	}
	value, err := f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.BeforeModelRequest, Execution: rsiplugin.Execution{RunID: run.RunID}})
	if err != nil || value.AdditionalContext[0].Text != "note:-old" {
		t.Fatal("uninstall destroyed retained code", value, err)
	}
	if err = f.host.Store.PutGraph(ctx, g); err == nil {
		t.Fatal("retired package accepted for new selection")
	}
}

func TestDependencyRPCUsesFrozenNamedBinding(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Hooks = nil
	m.Tools = nil
	upstream := install(t, f, m)
	m.PluginID = "steward.consumer"
	m.Requires = []rsiplugin.Requirement{{Binding: "upstream", Contract: "steward.echo/1"}}
	consumer := install(t, f, m)
	g, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{
		{InstanceID: "provider", PackageDigest: upstream.Digest, Config: json.RawMessage(`{"tag":"-provider"}`), StateNamespace: "provider", StateVersion: 1},
		{InstanceID: "consumer", PackageDigest: consumer.Digest, Config: json.RawMessage(`{"tag":"-consumer"}`), StateNamespace: "consumer", StateVersion: 1, Dependencies: []rsiplugin.Dependency{{Binding: "upstream", InstanceID: "provider", Contract: "steward.echo/1"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	activate(t, f, g, "dependencies")
	run, err := f.actor.Submit(ctx, "dep", "call dependency")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		value, err := f.host.Call(ctx, rsiplugin.Execution{RunID: run.RunID}, "consumer", "steward.echo/1", "dependency-call", json.RawMessage(`{"text":"dependency"}`))
		if err != nil || string(value) != `{"text":"nested-provider"}` {
			t.Fatal(string(value), err)
		}
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=?`, run.RunID).Scan(&count); err != nil || count != 2 {
		t.Fatal("dependency was reexecuted", count, err)
	}
}

func TestPluginCanProposeButCannotActivate(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Requests = []string{"evolution.propose"}
	p := install(t, f, m)
	active := graph(t, f, p, "-active")
	activate(t, f, active, "proposal-base")
	target := graph(t, f, p, "-candidate")
	// The independent owner's gates exist before a plugin submits a proposal.
	if err := f.host.Store.PutPolicy(ctx, rsi.Policy{ID: "proposal-policy", AllowedContracts: []string{"steward.echo/1"}, Cases: []rsi.Case{{InstanceID: "test", Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"test"}`), Expected: json.RawMessage(`{"text":"test-candidate"}`)}}}); err != nil {
		t.Fatal(err)
	}
	run, err := f.actor.Submit(ctx, "proposal", "request improvement")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"text": "propose", "path": target.Revision, "address": "proposal-policy"})
	value, err := f.host.Call(ctx, rsiplugin.Execution{RunID: run.RunID}, "test", "steward.echo/1", "proposal-call", input)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(value, &out); err != nil {
		t.Fatal(err)
	}
	candidate, err := f.host.Store.Candidate(ctx, out.Text)
	if err != nil || candidate.Baseline != active.Revision || candidate.Target != target.Revision {
		t.Fatal(candidate, err)
	}
	status, _ := f.host.Store.Status(ctx)
	if status.Revision != active.Revision {
		t.Fatal("proposal changed active graph", status)
	}
	// A package cannot authorize its newly requested capabilities through its own policy.
	if _, err = f.host.Verify(ctx, candidate.ID); !errors.Is(err, rsi.ErrVerification) {
		t.Fatal("missing hook/request grants accepted", err)
	}
	if err = f.host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	candidate.ID = "stopped-candidate"
	if err = f.host.Propose(ctx, candidate); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("stop allowed new proposal", err)
	}
}

func TestActivationRejectsEvidenceFromDifferentHost(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	g := graph(t, f, install(t, f, manifest()), "")
	a := decisionFor(t, f, g, "environment")
	evidence, err := f.host.Store.Decision(ctx, a.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Environment.HostDigest = "sha256:obsolete-host"
	a.DecisionID, err = f.host.Store.Decide(ctx, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Activate(ctx, a); !errors.Is(err, rsi.ErrVerification) {
		t.Fatal("foreign host evidence accepted", err)
	}
	status, _ := f.host.Store.Status(ctx)
	if status.Revision != "" || status.Epoch != 0 {
		t.Fatal("failed activation changed composition", status)
	}
}

func TestHookChainCannotRestoreRemovedCapability(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Tools = nil
	m.Hooks = []rsiplugin.Hook{{Stage: rsiplugin.SelectCapabilities}}
	p := install(t, f, m)
	g, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{
		{InstanceID: "first", PackageDigest: p.Digest, Config: json.RawMessage(`{"tag":"drop"}`), StateNamespace: "first", StateVersion: 1},
		{InstanceID: "second", PackageDigest: p.Digest, Config: json.RawMessage(`{"tag":"restore"}`), StateNamespace: "second", StateVersion: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	activate(t, f, g, "capability-chain")
	run, err := f.actor.Submit(ctx, "chain", "restricted work")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.host.Hook(ctx, rsiplugin.HookInput{Stage: rsiplugin.SelectCapabilities, Execution: rsiplugin.Execution{RunID: run.RunID}, ToolNames: []string{"plugin__echo"}})
	if !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("later plugin restored earlier restriction", err)
	}
}

func TestMissingDescribeReportsJoinedStatusWithoutPrivateStderr(t *testing.T) {
	requireSandbox(t)
	f := newFixture(t)
	m := manifest()
	m.Version = "99.0.0"
	pkg := install(t, f, m)
	binding := rsiplugin.Binding{InstanceID: "test", PackageDigest: pkg.Digest, Config: json.RawMessage(`{"tag":""}`), StateNamespace: "test", StateVersion: 1}
	var output rsiplugin.Result
	err := (rsi.Runner{Catalog: f.host.Catalog}).Invoke(context.Background(), binding, "call", rsiplugin.Call{}, &output)
	if !errors.Is(err, rsi.ErrUnknown) || !strings.Contains(err.Error(), "missing describe response") || !strings.Contains(err.Error(), "exit status 2") || strings.Contains(err.Error(), "PRIVATE_CANDIDATE") {
		t.Fatal("missing/unsafe startup diagnostic", err)
	}
}

type testProcessLimits struct {
	CPUSoft, CPUHard   uint64
	FileSoft, FileHard uint64
	FDSoft, FDHard     uint64
	ASSoft, ASHard     uint64
	RaiseDenied        bool
}
