package rsi

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ServiceInstance selects a uniquely provided contract for a Host-owned feature
// adapter. Explicit plugin-to-plugin dependencies continue to name an instance.
func (h Host) ServiceInstance(ctx context.Context, revision, contract string) (rsiplugin.Binding, error) {
	b, _, err := h.servicePackage(ctx, revision, contract)
	return b, err
}

// Resolve against immutable admission metadata, then verify the selected bytes.
func (h Host) servicePackage(ctx context.Context, revision, contract string) (rsiplugin.Binding, Package, error) {
	b, err := h.Store.ServiceBinding(ctx, revision, contract)
	if err != nil {
		return b, Package{}, err
	}
	p, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return b, p, err
	}
	if _, ok := findService(p.Manifest, contract); !ok {
		return b, p, ErrInvalid
	}
	return b, p, nil
}

// ReadService runs pure owner-side reads under a domain-supplied Fence, with no
// production Run, state writes, proposal authority or external-effect callbacks.
func (h Host) ReadService(ctx context.Context, revision, contract string, input json.RawMessage, inputs []InputData) (json.RawMessage, error) {
	return h.readService(ctx, revision, contract, input, inputs, false)
}

// ReadServiceStream is an explicit pure owner read of one immutable domain
// input whose existing source has no feature byte limit (account inventory).
// It grants the same declared input.read capability and per-chunk Fence as
// ReadService, with no resources, effects or state writes. Other call paths
// retain their existing byte limits; candidates cannot opt into this path.
func (h Host) ReadServiceStream(ctx context.Context, revision, contract string, input json.RawMessage, source InputData) (json.RawMessage, error) {
	return h.readService(ctx, revision, contract, input, []InputData{source}, true)
}

func (h Host) readService(ctx context.Context, revision, contract string, input json.RawMessage, inputs []InputData, streaming bool) (json.RawMessage, error) {
	if err := h.Store.CheckGraph(ctx, revision); err != nil {
		return nil, err
	}
	b, p, err := h.servicePackage(ctx, revision, contract)
	if err != nil {
		return nil, err
	}
	if hasRequest(p.Manifest, "state.run.write") || hasRequest(p.Manifest, "evolution.propose") {
		return nil, ErrRevoked
	}
	service, ok := findService(p.Manifest, contract)
	if !ok || !tool.Validate(string(service.InputSchema), string(input)) {
		return nil, ErrInvalid
	}
	var scope inputScope
	if streaming {
		scope, err = newImmutableInputScope(inputs[0])
	} else {
		scope, err = newInputScope(inputs)
	}
	if err != nil {
		return nil, err
	}
	if len(scope.descriptor) > 0 && !hasRequest(p.Manifest, "input.read") {
		return nil, ErrRevoked
	}
	runner := Runner{Catalog: h.Catalog, ReadInput: func(ctx context.Context, in rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
		if err := h.Store.CheckGraph(ctx, revision); err != nil {
			return rsiplugin.InputChunk{}, err
		}
		return scope.read(ctx, in)
	}}
	if streaming {
		// Exactly one pass over the owner-sized source, not an unlimited RPC budget.
		runner.inputReadLimit = 1 + (len(scope.data)-1)/rsiplugin.InputChunkBytes
	}
	var out rsiplugin.Result
	err = runner.Invoke(ctx, b, "call", rsiplugin.Call{OperationID: "owner-read", Execution: rsiplugin.Execution{GraphRevision: revision}, Contract: contract, Input: input, Inputs: scope.descriptor, State: rsiplugin.State{Value: p.Manifest.State.Initial}}, &out)
	if err != nil {
		// An input callback can reject a stale claim before the child reports its
		// transport fault. Pure reads preserve the authoritative revocation cause;
		// they must not turn cancellation/Forget into an unknown execution outcome.
		if authority := h.Store.CheckGraph(ctx, revision); authority != nil {
			return nil, authority
		}
		return nil, err
	}
	if out.State != nil || !tool.ValidateBounded(string(service.OutputSchema), string(out.Output), rsiplugin.MaxResultBytes) {
		return nil, ErrInvalid
	}
	if err = h.Store.CheckGraph(ctx, revision); err != nil {
		return nil, err
	}
	return out.Output, nil
}
