package rsiplugin

import (
	"context"
	"errors"
	"sort"
)

// Handlers is a small Go authoring adapter; service payloads are validated
// against their explicit schemas by Host before and after execution.
type Handlers struct {
	PluginID, Version  string
	OnPrepare          func(context.Context, PrepareInput) error
	Services           map[string]func(context.Context, Call) (Result, error)
	Hooks              map[Stage]func(context.Context, HookCall) (HookResult, error)
	OnDrain, OnDispose func(context.Context) error
}

func (h Handlers) Describe() Description {
	d := Description{Protocol: Protocol, PluginID: h.PluginID, Version: h.Version, Contracts: []string{}, Hooks: []Stage{}}
	for c := range h.Services {
		d.Contracts = append(d.Contracts, c)
	}
	for s := range h.Hooks {
		d.Hooks = append(d.Hooks, s)
	}
	sort.Strings(d.Contracts)
	sort.Slice(d.Hooks, func(i, j int) bool { return d.Hooks[i] < d.Hooks[j] })
	return d
}
func (h Handlers) Prepare(ctx context.Context, in PrepareInput) error {
	if h.OnPrepare != nil {
		return h.OnPrepare(ctx, in)
	}
	return nil
}
func (h Handlers) Call(ctx context.Context, in Call) (Result, error) {
	if f := h.Services[in.Contract]; f != nil {
		return f(ctx, in)
	}
	return Result{}, errors.New("unsupported service")
}
func (h Handlers) Hook(ctx context.Context, in HookCall) (HookResult, error) {
	if f := h.Hooks[in.Input.Stage]; f != nil {
		return f(ctx, in)
	}
	return HookResult{}, errors.New("unsupported hook")
}
func (h Handlers) Drain(ctx context.Context) error {
	if h.OnDrain != nil {
		return h.OnDrain(ctx)
	}
	return nil
}
func (h Handlers) Dispose(ctx context.Context) error {
	if h.OnDispose != nil {
		return h.OnDispose(ctx)
	}
	return nil
}
