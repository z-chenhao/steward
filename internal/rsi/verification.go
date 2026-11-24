package rsi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const verificationWorkers = 4

type verifiedCase struct {
	coverage string
	failure  string
	passed   bool
}

func failedVerificationInvocation(err error) verifiedCase {
	failure := "invoke"
	var startup *processStartupError
	if errors.As(err, &startup) {
		failure = fmt.Sprintf("invoke; joined_exit=%s; startup_report=%s", startup.status, startup.report)
	}
	return verifiedCase{failure: failure}
}

// Every case retains its own process, scratch, initial state and fixture ports.
// Bound top-level independent cases to four concurrent workers; existing nested
// dependency and computation limits still apply within each case. Native handlers
// have no concurrency contract, so their package's cases remain serial barriers
// in the original policy order; no native callback acquires a queue-time timeout.
func (h Host) verifyCases(ctx context.Context, graph rsiplugin.Graph, bindings map[string]rsiplugin.Binding, packages map[string]Package, cases []Case) []verifiedCase {
	results := make([]verifiedCase, len(cases))
	var running sync.WaitGroup
	sem := make(chan struct{}, verificationWorkers)
	for i, test := range cases {
		if hasRequest(packages[test.InstanceID].Manifest, "compute.ocr") {
			running.Wait()
			results[i] = h.verifyCase(ctx, graph, bindings, packages[test.InstanceID], test)
			continue
		}
		sem <- struct{}{}
		running.Add(1)
		go func(index int, test Case) {
			defer func() {
				<-sem
				running.Done()
			}()
			results[index] = h.verifyCase(ctx, graph, bindings, packages[test.InstanceID], test)
		}(i, test)
	}
	running.Wait()
	return results
}

func (h Host) verifyCase(ctx context.Context, graph rsiplugin.Graph, bindings map[string]rsiplugin.Binding, pkg Package, test Case) verifiedCase {
	b, ok := bindings[test.InstanceID]
	if !ok {
		return verifiedCase{failure: "binding"}
	}
	// Verify already loaded this exact graph's manifests. Invocation still checks
	// every package's sealed bytes immediately before and after each fresh process;
	// do not hash the same executable once more just to read its initial metadata.
	var err error
	state := rsiplugin.State{Value: pkg.Manifest.State.Initial}
	probe := &probe{catalog: h.Catalog, graph: graph, states: map[string]rsiplugin.State{}, remaining: 32}
	var actual json.RawMessage
	if test.Hook != nil {
		found := false
		for _, hook := range pkg.Manifest.Hooks {
			found = found || hook.Stage == test.Hook.Stage
		}
		var output rsiplugin.HookResult
		err = probe.runner(b).Invoke(ctx, b, "hook", rsiplugin.HookCall{OperationID: "verification", Input: visibleInput(*test.Hook, pkg.Manifest), State: state}, &output)
		if err != nil {
			return failedVerificationInvocation(err)
		}
		if !found || validateHook(pkg.Manifest, *test.Hook, output.Output) != nil || output.State != nil && (!hasRequest(pkg.Manifest, "state.run.write") || !tool.Validate(string(pkg.Manifest.State.Schema), string(*output.State))) {
			return verifiedCase{failure: "output"}
		}
		actual, _ = json.Marshal(output.Output)
	} else {
		service, found := findService(pkg.Manifest, test.Contract)
		if !found || !tool.Validate(string(service.InputSchema), string(test.Input)) {
			return verifiedCase{failure: "input"}
		}
		scope, scopeErr := newInputScope(test.Inputs)
		if scopeErr != nil || len(scope.descriptor) > 0 && !hasRequest(pkg.Manifest, "input.read") {
			return verifiedCase{failure: "input"}
		}
		runner := probe.runner(b)
		runner = h.withCompute(runner, &scope, nil, b, test.Contract, test.Input)
		runner.ReadInput = scope.read
		callCtx := ctx
		cancel := func() {}
		complete := func() bool { return true }
		if len(test.Resources) > 0 {
			var resourceErr error
			runner.resources, complete, resourceErr = verificationResources(test.Resources)
			if resourceErr != nil || !hasRequest(pkg.Manifest, "resource.call") {
				return verifiedCase{failure: "resource"}
			}
			callCtx, cancel = context.WithTimeout(ctx, time.Duration(pkg.Manifest.Runtime.TimeoutMS)*time.Millisecond)
		}
		var output rsiplugin.Result
		err = runner.Invoke(callCtx, b, "call", rsiplugin.Call{OperationID: "verification", Contract: test.Contract, Input: test.Input, State: state, Inputs: scope.descriptor}, &output)
		cancel()
		if err == nil && test.Contract == webextract.Contract {
			_, err = validateExtractComputation(&scope, output.Output)
		}
		if err != nil {
			return failedVerificationInvocation(err)
		}
		if !complete() || (test.Contract == webextract.Contract && !scope.computedAttempted) || !tool.ValidateBounded(string(service.OutputSchema), string(output.Output), rsiplugin.MaxResultBytes) || output.State != nil && (!hasRequest(pkg.Manifest, "state.run.write") || !tool.Validate(string(pkg.Manifest.State.Schema), string(*output.State))) {
			return verifiedCase{failure: "output"}
		}
		actual = output.Output
	}
	a, err := rsiplugin.Canonical(actual)
	if err != nil {
		return verifiedCase{failure: "canonical"}
	}
	expected, err := rsiplugin.Canonical(test.Expected)
	if err != nil || string(a) != string(expected) {
		return verifiedCase{failure: "expected"}
	}
	key := b.InstanceID + ":" + test.Contract
	if test.Hook != nil {
		key = b.InstanceID + ":hook:" + string(test.Hook.Stage)
	}
	return verifiedCase{coverage: key, passed: true}
}
