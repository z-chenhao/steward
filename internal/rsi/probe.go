package rsi

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// probe owns disposable verification state. Dependency tests never read or
// mutate a production Run's state, even when the candidate requests a service.
type probe struct {
	catalog   Catalog
	graph     rsiplugin.Graph
	states    map[string]rsiplugin.State
	remaining int
}

func (p *probe) runner(b rsiplugin.Binding) Runner {
	return Runner{Catalog: p.catalog, Propose: func(_ context.Context, in rsiplugin.Proposal) (rsiplugin.ProposalReceipt, error) {
		pkg, err := p.catalog.Load(b.PackageDigest)
		if err != nil {
			return rsiplugin.ProposalReceipt{}, err
		}
		if !hasRequest(pkg.Manifest, "evolution.propose") || !contentDigest.MatchString(in.Target) || !identifier.MatchString(in.PolicyID) || in.Goal == "" || len(in.Goal) > 2048 {
			return rsiplugin.ProposalReceipt{}, ErrInvalid
		}
		return rsiplugin.ProposalReceipt{CandidateID: "verification-candidate"}, nil
	}, Dependency: func(ctx context.Context, in rsiplugin.DependencyCall) (json.RawMessage, error) {
		if p.remaining <= 0 {
			return nil, ErrInvalid
		}
		p.remaining--
		for _, d := range b.Dependencies {
			if d.Binding != in.Binding {
				continue
			}
			for _, target := range p.graph.Bindings {
				if target.InstanceID != d.InstanceID {
					continue
				}
				pkg, err := p.catalog.Load(target.PackageDigest)
				if err != nil {
					return nil, err
				}
				s, ok := findService(pkg.Manifest, d.Contract)
				if !ok || !tool.Validate(string(s.InputSchema), string(in.Input)) {
					return nil, ErrInvalid
				}
				state, ok := p.states[target.InstanceID]
				if !ok {
					state = rsiplugin.State{Value: pkg.Manifest.State.Initial}
				}
				var out rsiplugin.Result
				if err = p.runner(target).Invoke(ctx, target, "call", rsiplugin.Call{OperationID: in.Key, Contract: d.Contract, Input: in.Input, State: state}, &out); err != nil {
					return nil, err
				}
				if !tool.Validate(string(s.OutputSchema), string(out.Output)) {
					return nil, ErrInvalid
				}
				if out.State != nil {
					if !hasRequest(pkg.Manifest, "state.run.write") || !tool.Validate(string(pkg.Manifest.State.Schema), string(*out.State)) {
						return nil, ErrInvalid
					}
					state.Version++
					state.Value = *out.State
				}
				p.states[target.InstanceID] = state
				return out.Output, nil
			}
		}
		return nil, ErrNotFound
	}}
}
