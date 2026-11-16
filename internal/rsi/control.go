package rsi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type Case struct {
	InstanceID string               `json:"instance_id"`
	Contract   string               `json:"contract,omitempty"`
	Input      json.RawMessage      `json:"input,omitempty"`
	Hook       *rsiplugin.HookInput `json:"hook,omitempty"`
	Expected   json.RawMessage      `json:"expected"`
	Inputs     []InputData          `json:"inputs,omitempty"`
	Resources  []ResourceFixture    `json:"resources,omitempty"`
}

// Policy enters through the authenticated operator control plane, never from a
// candidate package. These are bounded functional gates, not a universal score.
type Policy struct {
	ID               string            `json:"id"`
	Cases            []Case            `json:"cases"`
	AllowedHooks     []rsiplugin.Stage `json:"allowed_hooks"`
	AllowedContracts []string          `json:"allowed_contracts"`
	AllowedRequests  []string          `json:"allowed_requests,omitempty"`
}
type Candidate struct {
	ID       string `json:"candidate_id"`
	Baseline string `json:"baseline"`
	Target   string `json:"target"`
	PolicyID string `json:"policy_id"`
	Goal     string `json:"goal"`
}
type Evidence struct {
	CandidateID  string      `json:"candidate_id"`
	Target       string      `json:"target"`
	PolicyDigest string      `json:"policy_digest"`
	Passed       bool        `json:"passed"`
	Cases        int         `json:"cases"`
	Environment  Environment `json:"environment"`
}
type Activation struct {
	OperationID string `json:"operation_id"`
	Expected    string `json:"expected_graph_revision"`
	Target      string `json:"target_graph_revision"`
	DecisionID  string `json:"decision_ref"`
}
type Receipt struct {
	OperationID string `json:"operation_id"`
	Previous    string `json:"previous_graph_revision"`
	Current     string `json:"graph_revision"`
	Epoch       int64  `json:"epoch"`
}
type Status struct {
	Revision string `json:"graph_revision"`
	Epoch    int64  `json:"epoch"`
	Stopped  bool   `json:"stopped"`
}
type Audit struct {
	Sequence  int64           `json:"sequence"`
	Kind      string          `json:"kind"`
	Reference string          `json:"reference"`
	Detail    json.RawMessage `json:"detail"`
}

type CallStatus struct {
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
	Completed   bool   `json:"completed"`
}

// Store is the Host-only control port. Implementations must atomically commit
// activation, its idempotent receipt and audit; plugins never receive this port.
type Store interface {
	Install(context.Context, Package) error
	PutGraph(context.Context, ResolvedGraph) error
	Graph(context.Context, string) (ResolvedGraph, error)
	PutPolicy(context.Context, Policy) error
	Policy(context.Context, string) (Policy, error)
	Propose(context.Context, Candidate) error
	Candidate(context.Context, string) (Candidate, error)
	Decide(context.Context, Evidence) (string, error)
	Decision(context.Context, string) (Evidence, error)
	Activate(context.Context, Activation) (Receipt, error)
	ActivationReceipt(context.Context, Activation) (Receipt, error)
	Status(context.Context) (Status, error)
	Stop(context.Context, bool) error
	Quarantine(context.Context, string) error
	Deactivate(context.Context, string, string) (Receipt, error)
	Retire(context.Context, string) error
	Audit(context.Context) ([]Audit, error)
	Binding(context.Context, string) (string, error)
	CheckGraph(context.Context, string) error
	BeginCall(context.Context, string, string, string, string) (json.RawMessage, *rsiplugin.State, error)
	FinishCall(context.Context, string, string, string, rsiplugin.State, *json.RawMessage, json.RawMessage) error
}
type Host struct {
	Catalog Catalog
	Store   Store
	Compute *Compute
	Parser  *parserresource.Process
}

func (h Host) Activate(ctx context.Context, a Activation) (Receipt, error) {
	if receipt, err := h.Store.ActivationReceipt(ctx, a); err == nil {
		return receipt, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Receipt{}, err
	}
	evidence, err := h.Store.Decision(ctx, a.DecisionID)
	if err != nil {
		return Receipt{}, err
	}
	g, err := h.Store.Graph(ctx, a.Target)
	if err != nil {
		return Receipt{}, err
	}
	current, err := h.environment(ctx, g)
	if err != nil {
		return Receipt{}, err
	}
	if evidence.Environment != current {
		return Receipt{}, ErrVerification
	}
	resolved, err := h.Catalog.Resolve(g.Graph)
	if err != nil || resolved.Revision != a.Target {
		return Receipt{}, ErrInvalid
	}
	return h.Store.Activate(ctx, a)
}

func (h Host) Install(ctx context.Context, source string) (Package, error) {
	p, err := h.Catalog.Install(ctx, source)
	if err == nil {
		err = h.Store.Install(ctx, p)
	}
	return p, err
}
func (h Host) Prepare(ctx context.Context, g rsiplugin.Graph) (ResolvedGraph, error) {
	r, err := h.Catalog.Resolve(g)
	if err != nil {
		return r, err
	}
	if err = h.prepareBindings(ctx, r); err != nil {
		return r, err
	}

	return r, h.Store.PutGraph(ctx, r)
}
func (h Host) Propose(ctx context.Context, c Candidate) error {
	if !identifier.MatchString(c.ID) || len(c.Goal) == 0 || len(c.Goal) > 2048 {
		return ErrInvalid
	}
	if _, err := h.Store.Graph(ctx, c.Target); err != nil {
		return err
	}
	if _, err := h.Store.Policy(ctx, c.PolicyID); err != nil {
		return err
	}
	return h.Store.Propose(ctx, c)
}
func (h Host) Verify(ctx context.Context, id string) (string, error) {
	c, err := h.Store.Candidate(ctx, id)
	if err != nil {
		return "", err
	}
	g, err := h.Store.Graph(ctx, c.Target)
	if err != nil {
		return "", err
	}
	started, err := h.environment(ctx, g)
	if err != nil {
		return "", err
	}
	resolved, err := h.Catalog.Resolve(g.Graph)
	if err != nil || resolved.Revision != g.Revision {
		return "", ErrInvalid
	}
	p, err := h.Store.Policy(ctx, c.PolicyID)
	if err != nil {
		return "", err
	}
	pd, err := rsiplugin.Digest("steward.rsi.policy/1", p)
	if err != nil {
		return "", err
	}
	passed := len(p.Cases) > 0
	covered := map[string]bool{}
	allowedHooks := map[rsiplugin.Stage]bool{}
	for _, s := range p.AllowedHooks {
		allowedHooks[s] = true
	}
	allowedContracts := map[string]bool{}
	for _, s := range p.AllowedContracts {
		allowedContracts[s] = true
	}
	allowedRequests := map[string]bool{}
	for _, s := range p.AllowedRequests {
		allowedRequests[s] = true
	}
	bindings := map[string]rsiplugin.Binding{}
	packages := map[string]Package{}
	for _, b := range g.Graph.Bindings {
		bindings[b.InstanceID] = b
		pkg, e := h.Catalog.Load(b.PackageDigest)
		if e != nil {
			return "", e
		}
		packages[b.InstanceID] = pkg
		for _, s := range pkg.Manifest.Provides {
			passed = passed && allowedContracts[s.Contract]
		}
		for _, s := range pkg.Manifest.Hooks {
			passed = passed && allowedHooks[s.Stage]
		}
		for _, request := range pkg.Manifest.Requests {
			passed = passed && allowedRequests[request]
		}
	}
	for _, result := range h.verifyCases(ctx, g.Graph, bindings, packages, p.Cases) {
		if result.err != nil {
			return "", result.err
		}
		passed = passed && result.passed
		if result.passed {
			covered[result.coverage] = true
		}
	}
	for _, b := range g.Graph.Bindings {
		pkg, e := h.Catalog.Load(b.PackageDigest)
		if e != nil {
			return "", e
		}
		for _, service := range pkg.Manifest.Provides {
			passed = passed && covered[b.InstanceID+":"+service.Contract]
		}
		for _, hook := range pkg.Manifest.Hooks {
			passed = passed && covered[b.InstanceID+":hook:"+string(hook.Stage)]
		}
	}
	env, err := h.environment(ctx, g)
	if err != nil {
		return "", err
	}
	if env != started {
		return "", ErrVerification
	}
	decision, err := h.Store.Decide(ctx, Evidence{CandidateID: c.ID, Target: g.Revision, PolicyDigest: pd, Passed: passed, Cases: len(p.Cases), Environment: env})
	if err != nil {
		return "", err
	}
	if !passed {
		return decision, ErrVerification
	}
	return decision, nil
}
func findService(m rsiplugin.Manifest, name string) (rsiplugin.Service, bool) {
	for _, s := range m.Provides {
		if s.Contract == name {
			return s, true
		}
	}
	return rsiplugin.Service{}, false
}

// Hook applies an ordered, version-pinned chain. The journal accepts suggestions
// once; retries reuse them instead of executing a different plugin version.
func (h Host) Hook(ctx context.Context, input rsiplugin.HookInput) (rsiplugin.HookOutput, error) {
	var combined rsiplugin.HookOutput
	revision, err := h.Store.Binding(ctx, input.Execution.RunID)
	if err != nil {
		return combined, err
	}
	if revision == "" {
		return combined, nil
	}
	input.Execution.GraphRevision = revision
	g, err := h.Store.Graph(ctx, revision)
	if err != nil {
		return combined, err
	}
	for _, id := range g.Order {
		for _, b := range g.Graph.Bindings {
			if b.InstanceID != id {
				continue
			}
			p, e := h.Catalog.Load(b.PackageDigest)
			if e != nil {
				return combined, e
			}
			found := false
			for _, hook := range p.Manifest.Hooks {
				found = found || hook.Stage == input.Stage
			}
			if !found {
				continue
			}
			visible := visibleInput(input, p.Manifest)
			key, e := rsiplugin.Digest("steward.rsi.hook/1", struct {
				Input    rsiplugin.HookInput
				Instance string
			}{visible, b.InstanceID})
			if e != nil {
				return combined, ErrInvalid
			}
			var output rsiplugin.HookOutput
			err = h.invoke(ctx, input.Execution, b, key, visible, func(state rsiplugin.State) (json.RawMessage, *json.RawMessage, error) {
				var result rsiplugin.HookResult
				e := h.runner(b, input.Execution, key).Invoke(ctx, b, "hook", rsiplugin.HookCall{OperationID: key, Input: visible, State: state}, &result)
				if e == nil {
					e = validateHook(p.Manifest, input, result.Output)
				}
				raw, _ := json.Marshal(result.Output)
				return raw, result.State, e
			}, &output)
			if err != nil {
				return combined, err
			}
			combined.Reject = combined.Reject || output.Reject
			if output.Reason != "" {
				combined.Reason = output.Reason
			}
			combined.AdditionalContext = append(combined.AdditionalContext, output.AdditionalContext...)
			if len(output.Arguments) > 0 {
				combined.Arguments = output.Arguments
				if input.ToolCall != nil {
					copy := *input.ToolCall
					copy.Arguments = output.Arguments
					input.ToolCall = &copy
				}
			}
			if output.Text != nil {
				combined.Text = output.Text
				input.Text = *output.Text
			}
			if output.ToolNames != nil {
				combined.ToolNames = output.ToolNames
				input.ToolNames = *output.ToolNames
			}
		}
	}
	return combined, combined.Validate(input.Stage)
}
func (h Host) Call(ctx context.Context, execution rsiplugin.Execution, instance, contract, key string, input json.RawMessage) (json.RawMessage, error) {
	return h.CallWithInput(ctx, execution, instance, contract, key, input, nil)
}

func (h Host) CallWithInput(ctx context.Context, execution rsiplugin.Execution, instance, contract, key string, input json.RawMessage, inputs []InputData) (json.RawMessage, error) {
	scope, err := newInputScope(inputs)
	if err != nil {
		return nil, err
	}
	revision, err := h.Store.Binding(ctx, execution.RunID)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return nil, ErrNotFound
	}
	g, err := h.Store.Graph(ctx, revision)
	if err != nil {
		return nil, err
	}
	execution.GraphRevision = revision
	for _, b := range g.Graph.Bindings {
		if b.InstanceID != instance {
			continue
		}
		p, e := h.Catalog.Load(b.PackageDigest)
		if e != nil {
			return nil, e
		}
		service, ok := findService(p.Manifest, contract)
		if len(scope.descriptor) > 0 && !hasRequest(p.Manifest, "input.read") {
			return nil, ErrRevoked
		}
		if !ok || !tool.Validate(string(service.InputSchema), string(input)) {
			return nil, ErrInvalid
		}
		var output json.RawMessage
		err = h.invoke(ctx, execution, b, key, struct {
			Contract string
			Input    json.RawMessage
			Inputs   []rsiplugin.Input `json:",omitempty"`
		}{contract, input, scope.descriptor}, func(state rsiplugin.State) (json.RawMessage, *json.RawMessage, error) {
			var result rsiplugin.Result
			runner := h.withCompute(h.runner(b, execution, key), &scope, func(ctx context.Context) error {
				current, err := h.Store.Binding(ctx, execution.RunID)
				if err != nil {
					return err
				}
				if current != execution.GraphRevision {
					return ErrRevoked
				}
				return nil
			}, b, contract, input)
			runner.ReadInput = func(ctx context.Context, in rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
				current, err := h.Store.Binding(ctx, execution.RunID)
				if err != nil {
					return rsiplugin.InputChunk{}, err
				}
				if current != execution.GraphRevision {
					return rsiplugin.InputChunk{}, ErrRevoked
				}
				return scope.read(ctx, in)
			}
			e := runner.Invoke(ctx, b, "call", rsiplugin.Call{OperationID: key, Execution: execution, Contract: contract, Input: input, State: state, Inputs: scope.descriptor}, &result)
			if e == nil && !tool.ValidateBounded(string(service.OutputSchema), string(result.Output), rsiplugin.MaxResultBytes) {
				e = ErrInvalid
			}
			return result.Output, result.State, e
		}, &output)
		return output, err
	}
	return nil, ErrNotFound
}
func (h Host) invoke(ctx context.Context, execution rsiplugin.Execution, b rsiplugin.Binding, key string, input any, run func(rsiplugin.State) (json.RawMessage, *json.RawMessage, error), output any) (err error) {
	if key == "" || len(key) > 256 {
		return ErrInvalid
	}
	hash, err := rsiplugin.Digest("steward.rsi.input/1", input)
	if err != nil {
		return ErrInvalid
	}
	raw, state, err := h.Store.BeginCall(ctx, execution.RunID, b.InstanceID, key, hash)
	if err != nil {
		return err
	}
	if state == nil {
		return rsiplugin.DecodeResult(raw, output)
	}
	defer func() {
		if err != nil {
			err = errors.Join(ErrUnknown, err)
		}
	}()
	value, change, err := run(*state)
	if err != nil {
		return err
	}
	p, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return err
	}
	if change != nil && (!hasRequest(p.Manifest, "state.run.write") || !tool.Validate(string(p.Manifest.State.Schema), string(*change))) {
		return ErrInvalid
	}
	if err = h.Store.FinishCall(ctx, execution.RunID, b.InstanceID, key, *state, change, value); err != nil {
		return err
	}
	return rsiplugin.DecodeResult(value, output)
}

func hasRequest(m rsiplugin.Manifest, name string) bool {
	for _, r := range m.Requests {
		if r == name {
			return true
		}
	}
	return false
}

func validateHook(m rsiplugin.Manifest, input rsiplugin.HookInput, out rsiplugin.HookOutput) error {
	stage := input.Stage
	if out.ToolNames != nil {
		available := map[string]bool{}
		for _, name := range input.ToolNames {
			available[name] = true
		}
		selected := map[string]bool{}
		for _, name := range *out.ToolNames {
			if !available[name] || selected[name] {
				return ErrInvalid
			}
			selected[name] = true
		}
	}
	if err := out.Validate(stage); err != nil {
		return err
	}
	if len(out.Arguments) > 0 && !hasRequest(m, "run.tool.read") {
		return ErrInvalid
	}
	permission := "run.reply.read"
	if stage == rsiplugin.ProjectToolResult {
		permission = "run.tool.read"
	}
	if out.Text != nil && !hasRequest(m, permission) {
		return ErrInvalid
	}
	return nil
}

type dependencyDepthKey struct{}

func (h Host) runner(b rsiplugin.Binding, execution rsiplugin.Execution, parent string) Runner {
	return Runner{Catalog: h.Catalog, Propose: func(ctx context.Context, p rsiplugin.Proposal) (rsiplugin.ProposalReceipt, error) {
		pkg, err := h.Catalog.Load(b.PackageDigest)
		if err != nil {
			return rsiplugin.ProposalReceipt{}, err
		}
		if !hasRequest(pkg.Manifest, "evolution.propose") {
			return rsiplugin.ProposalReceipt{}, ErrRevoked
		}
		digest, err := rsiplugin.Digest("steward.rsi.proposal/1", struct {
			Execution rsiplugin.Execution
			Instance  string
			Proposal  rsiplugin.Proposal
		}{execution, b.InstanceID, p})
		if err != nil {
			return rsiplugin.ProposalReceipt{}, err
		}
		id := "candidate-" + strings.TrimPrefix(digest, "sha256:")
		err = h.Propose(ctx, Candidate{ID: id, Baseline: execution.GraphRevision, Target: p.Target, PolicyID: p.PolicyID, Goal: p.Goal})
		return rsiplugin.ProposalReceipt{CandidateID: id}, err
	}, Dependency: func(ctx context.Context, d rsiplugin.DependencyCall) (json.RawMessage, error) {
		depth, _ := ctx.Value(dependencyDepthKey{}).(int)
		if depth >= 8 {
			return nil, ErrInvalid
		}
		ctx = context.WithValue(ctx, dependencyDepthKey{}, depth+1)
		for _, binding := range b.Dependencies {
			if binding.Binding == d.Binding {
				key, err := rsiplugin.Digest("steward.rsi.dependency/1", struct{ Parent, Binding, Key string }{parent, d.Binding, d.Key})
				if err != nil {
					return nil, err
				}
				return h.Call(ctx, execution, binding.InstanceID, binding.Contract, key, d.Input)
			}
		}
		return nil, ErrNotFound
	}}
}
func visibleInput(input rsiplugin.HookInput, m rsiplugin.Manifest) rsiplugin.HookInput {
	if !hasRequest(m, "run.context.read") {
		input.Messages = nil
	}
	if input.ToolCall != nil && !hasRequest(m, "run.tool.read") {
		copy := *input.ToolCall
		copy.Arguments = json.RawMessage("null")
		input.ToolCall = &copy
	}
	permission := "run.reply.read"
	if input.Stage == rsiplugin.ToolResult || input.Stage == rsiplugin.ProjectToolResult {
		permission = "run.tool.read"
	}
	if !hasRequest(m, permission) {
		input.Text = ""
	}
	return input
}

// The standard24 composition has 135 owner cases. Keep a finite control-plane
// bound without dropping cases; process concurrency and per-case limits remain fixed.
// The current 32-package distribution requires 319 independent Owner cases.
// Keep the policy bounded; per-case execution and control-byte limits remain.
const maxVerificationCases = 512

func ValidatePolicy(p Policy) error {
	if !identifier.MatchString(p.ID) || len(p.Cases) == 0 || len(p.Cases) > maxVerificationCases {
		return ErrInvalid
	}
	for _, c := range p.Cases {
		if !identifier.MatchString(c.InstanceID) || !json.Valid(c.Expected) || (c.Hook == nil) == (c.Contract == "") {
			return ErrInvalid
		}
		if len(c.Inputs) > 1 || c.Hook != nil && len(c.Inputs) > 0 {
			return ErrInvalid
		}
		for _, input := range c.Inputs {
			if !identifier.MatchString(input.ID) || len(input.Data) > rsiplugin.MaxInputBytes {
				return ErrInvalid
			}
		}
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > rsiplugin.MaxControlBytes {
		return ErrInvalid
	}
	return nil
}
