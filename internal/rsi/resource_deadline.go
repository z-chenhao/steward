package rsi

import (
	"context"
	"sync"
	"time"
)

// resourceDeadline counts plugin-active wall time cumulatively. Trusted owner IO
// uses the original operation deadline; ExecPlugin's hard CPU quota still applies
// even to background work while the plugin is waiting for a resource response.
type resourceDeadline struct {
	mu        sync.Mutex
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	expired   bool
	cancel    context.CancelCauseFunc
}

func newResourceDeadline(duration time.Duration, cancel context.CancelCauseFunc) *resourceDeadline {
	b := &resourceDeadline{remaining: duration, cancel: cancel}
	b.resume()
	return b
}
func (b *resourceDeadline) expire() {
	b.mu.Lock()
	b.expired = true
	b.mu.Unlock()
	b.cancel(context.DeadlineExceeded)
}
func (b *resourceDeadline) pause() error {
	b.mu.Lock()
	if b.expired || !b.timer.Stop() {
		b.expired = true
		b.mu.Unlock()
		b.cancel(context.DeadlineExceeded)
		return context.DeadlineExceeded
	}
	b.remaining -= time.Since(b.started)
	if b.remaining <= 0 {
		b.expired = true
		b.mu.Unlock()
		b.cancel(context.DeadlineExceeded)
		return context.DeadlineExceeded
	}
	b.mu.Unlock()
	return nil
}
func (b *resourceDeadline) resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.expired {
		return
	}
	b.started = time.Now()
	b.timer = time.AfterFunc(b.remaining, b.expire)
}
func (b *resourceDeadline) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timer.Stop()
}
