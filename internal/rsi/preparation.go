package rsi

import (
	"context"
	"sync"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Preparation receives no effect, dependency, proposal or computation ports.
// Bound independent sandbox starts; retain the resolved dependency barriers and
// publish the graph only after every fresh process passes its integrity fences.
func (h Host) prepareBindings(ctx context.Context, r ResolvedGraph) error {
	const workers = 4
	bindings := map[string]rsiplugin.Binding{}
	for _, b := range r.Graph.Bindings {
		bindings[b.InstanceID] = b
	}
	done := map[string]bool{}
	for len(done) < len(r.Order) {
		batch := []rsiplugin.Binding{}
		for _, id := range r.Order {
			if done[id] {
				continue
			}
			b := bindings[id]
			ready := true
			for _, dep := range b.Dependencies {
				ready = ready && done[dep.InstanceID]
			}
			if ready {
				batch = append(batch, b)
			}
			if len(batch) == workers {
				break
			}
		}
		if len(batch) == 0 {
			return ErrInvalid
		}
		results := make([]error, len(batch))
		var running sync.WaitGroup
		for i, b := range batch {
			running.Add(1)
			go func(i int, b rsiplugin.Binding) {
				defer running.Done()
				results[i] = (Runner{Catalog: h.Catalog}).Invoke(ctx, b, "prepare", nil, nil)
			}(i, b)
		}
		running.Wait()
		for i, b := range batch {
			if results[i] != nil {
				return results[i]
			}
			done[b.InstanceID] = true
		}
	}
	return nil
}
