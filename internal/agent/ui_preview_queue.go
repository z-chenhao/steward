package agent

import (
	"context"
	"sync"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type previewSnapshot struct{ key, raw string }

// The model stream never waits on an isolated preview process. One worker keeps
// the latest prefix per call in first-seen order; superseded drafts are ephemeral.
// Stop cancels and joins it before generation transitions or prefix removal.
type uiPreviewQueue struct {
	ctx     context.Context
	cancel  context.CancelFunc
	project func(context.Context, string) (ui.Preview, bool)
	publish func(string, ui.Preview)
	mu      sync.Mutex
	pending map[string]string
	order   []string
	wake    chan struct{}
	done    chan struct{}
}

func newUIPreviewQueue(ctx context.Context, project func(context.Context, string) (ui.Preview, bool), publish func(string, ui.Preview)) *uiPreviewQueue {
	ctx, cancel := context.WithCancel(ctx)
	q := &uiPreviewQueue{ctx: ctx, cancel: cancel, project: project, publish: publish, pending: map[string]string{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go q.run()
	return q
}

func (q *uiPreviewQueue) submit(key, raw string) {
	if q.ctx.Err() != nil {
		return
	}
	q.mu.Lock()
	known := false
	for _, existing := range q.order {
		if existing == key {
			known = true
			break
		}
	}
	if !known {
		if len(q.order) >= 16 {
			q.mu.Unlock()
			return
		}
		q.order = append(q.order, key)
	}
	q.pending[key] = raw
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *uiPreviewQueue) run() {
	defer close(q.done)
	for {
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		}
		q.mu.Lock()
		snapshots := make([]previewSnapshot, 0, len(q.pending))
		for _, key := range q.order {
			if raw, ok := q.pending[key]; ok {
				snapshots = append(snapshots, previewSnapshot{key, raw})
			}
		}
		clear(q.pending)
		q.mu.Unlock()
		for _, snapshot := range snapshots {
			if q.ctx.Err() != nil {
				return
			}
			value, valid := q.project(q.ctx, snapshot.raw)
			if valid && q.ctx.Err() == nil {
				q.publish(snapshot.key, value)
			}
		}
	}
}

func (q *uiPreviewQueue) stop() { q.cancel(); <-q.done }
