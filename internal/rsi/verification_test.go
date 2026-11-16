package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Count only this test process's children, excluding the observer itself. No
// unrelated command arguments, files or user data enter the fixture evidence.
func observeVerificationProcesses() func() (int, error) {
	stop, done := make(chan struct{}), make(chan struct{})
	maximum := 0
	var failure error
	go func() {
		defer close(done)
		for {
			command := exec.Command("ps", "-axo", "pid=,ppid=")
			output, err := command.Output()
			if err != nil {
				failure = err
				return
			}
			count := 0
			for _, row := range strings.Split(string(output), "\n") {
				var pid, parent int
				if _, err := fmt.Sscan(row, &pid, &parent); err == nil && parent == os.Getpid() && pid != command.Process.Pid {
					count++
				}
			}
			maximum = max(maximum, count)
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return func() (int, error) { close(stop); <-done; return maximum, failure }
}

func TestVerificationBoundedFreshProcessesAndLateFailure(t *testing.T) {
	requireSandbox(t)
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint("native-compute=", native), func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			m := manifest()
			m.Hooks, m.Tools = nil, nil
			if native {
				m.Requests = []string{"compute.ocr"}
			}
			pkg := install(t, f, m)
			g := graph(t, f, pkg, "")
			policy := rsi.Policy{ID: "bounded-verification", AllowedContracts: []string{"steward.echo/1"}, AllowedRequests: m.Requests}
			for i := 0; i < 9; i++ {
				// A process-local counter must be one in every case. Reusing a
				// resident candidate to save startup time violates this check.
				policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "test", Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"verification-delay"}`), Expected: json.RawMessage(`{"text":"1"}`)})
			}
			if err := f.host.Store.PutPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			candidate := rsi.Candidate{ID: "bounded-verification", Target: g.Revision, PolicyID: policy.ID, Goal: "check isolated verification"}
			if err := f.host.Propose(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			stop := observeVerificationProcesses()
			_, err := f.host.Verify(ctx, candidate.ID)
			maximum, observationErr := stop()
			if err != nil || observationErr != nil {
				t.Fatal(err, observationErr)
			}
			if native && maximum != 1 || !native && (maximum < 2 || maximum > 4) {
				t.Fatal("wrong verification process bound", maximum, "native", native)
			}
			if native {
				return
			}
			// A failure beyond the first two batches must still participate in
			// the decision; covering the service with an early case is not enough.
			policy.ID = "bounded-verification-late-failure"
			policy.Cases[len(policy.Cases)-1].Expected = json.RawMessage(`{"text":"wrong"}`)
			if err := f.host.Store.PutPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			candidate.ID, candidate.PolicyID = policy.ID, policy.ID
			if err := f.host.Propose(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			if _, err := f.host.Verify(ctx, candidate.ID); !errors.Is(err, rsi.ErrVerification) {
				t.Fatal("late failure was dropped", err)
			}
		})
	}
}

// Standard32 needs 319 independent cases, beyond the former whole-policy limit.
// A late failure must reject activation even when an earlier case covered it.
func TestVerificationBeyondFormerCaseLimitIncludesLateFailure(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	m := manifest()
	m.Hooks, m.Tools = nil, nil
	pkg := install(t, f, m)
	g := graph(t, f, pkg, "")
	policy := rsi.Policy{ID: "standard32-late-failure", AllowedContracts: []string{"steward.echo/1"}}
	for i := 0; i < 319; i++ {
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "test", Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"owner"}`), Expected: json.RawMessage(`{"text":"owner"}`)})
	}
	policy.Cases[318].Expected = json.RawMessage(`{"text":"wrong"}`)
	if err := f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Propose(ctx, rsi.Candidate{ID: policy.ID, Target: g.Revision, PolicyID: policy.ID, Goal: "include every owner case"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Verify(ctx, policy.ID); !errors.Is(err, rsi.ErrVerification) {
		t.Fatal("late case did not reject verification", err)
	}
}

func TestVerificationPolicyCaseBound(t *testing.T) {
	for _, n := range []int{0, 128, 135, 256, 257, 319, 512, 513} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			policy := rsi.Policy{ID: "case-bound", Cases: make([]rsi.Case, n)}
			for i := range policy.Cases {
				policy.Cases[i] = rsi.Case{InstanceID: "test", Contract: "steward.echo/1", Expected: json.RawMessage(`{}`)}
			}
			err := rsi.ValidatePolicy(policy)
			if want := n > 0 && n <= 512; (err == nil) != want {
				t.Fatal(n, err)
			}
		})
	}
}

func TestPreparationBoundedProcessesDependenciesAndLateFailure(t *testing.T) {
	requireSandbox(t)
	for _, chain := range []bool{false, true} {
		t.Run(fmt.Sprint("dependency-chain=", chain), func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			m := manifest()
			m.Hooks, m.Tools = nil, nil
			root := install(t, f, m)
			consumer := root
			if chain {
				m.PluginID = "steward.prepare-consumer"
				m.Requires = []rsiplugin.Requirement{{Binding: "upstream", Contract: "steward.echo/1"}}
				consumer = install(t, f, m)
			}
			g := rsiplugin.Graph{Protocol: rsiplugin.Protocol}
			for i := 0; i < 9; i++ {
				id := fmt.Sprintf("node-%d", i)
				pkg := consumer
				if i == 0 {
					pkg = root
				}
				binding := rsiplugin.Binding{InstanceID: id, PackageDigest: pkg.Digest, Config: json.RawMessage(`{"tag":"prepare-delay"}`), StateNamespace: id, StateVersion: 1}
				if chain && i > 0 {
					binding.Dependencies = []rsiplugin.Dependency{{Binding: "upstream", InstanceID: fmt.Sprintf("node-%d", i-1), Contract: "steward.echo/1"}}
				}
				g.Bindings = append(g.Bindings, binding)
			}
			stop := observeVerificationProcesses()
			prepared, err := f.host.Prepare(ctx, g)
			maximum, observed := stop()
			if err != nil || observed != nil {
				t.Fatal(err, observed)
			}
			if chain && maximum != 1 || !chain && (maximum < 2 || maximum > 4) {
				t.Fatal("prepare violated dependency or process bound", chain, maximum)
			}
			if _, err = f.host.Store.Graph(ctx, prepared.Revision); err != nil {
				t.Fatal(err)
			}
			// An independent failure in the last batch must never publish its graph.
			g.Bindings[8].Config = json.RawMessage(`{"tag":"prepare-fail"}`)
			rejected, err := f.host.Prepare(ctx, g)
			if err == nil {
				t.Fatal("late preparation failure accepted")
			}
			if _, err = f.host.Store.Graph(ctx, rejected.Revision); !errors.Is(err, rsi.ErrNotFound) {
				t.Fatal("partial prepared graph published", err)
			}
		})
	}
}
