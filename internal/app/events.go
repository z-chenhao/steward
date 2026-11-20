package app

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/event"
	eventnats "github.com/z-chenhao/assistant/internal/event/nats"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

// WaitForRuntime is the interactive selection boundary, not a model API.
// Selection persists first; its UI completes only when work admission is ready.
func (a *App) WaitForRuntime(ctx context.Context) error {
	if !a.eventsRequired.Load() {
		return nil
	}
	if d, e := a.providerDiagnostics(ctx); e != nil || !d.AgentIngressEnabled {
		return agent.ErrSetup
	}
	select {
	case a.runtimeWake <- struct{}{}:
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !a.eventsReady.Load() {
		select {
		case <-a.runtimeDone:
			return agent.ErrSetup
		case <-ctx.Done():
			return event.ErrUnavailable
		case <-ticker.C:
		}
	}
	return nil
}

func (a *App) configuredOutbox(cfg config.Config) *eventsqlite.Store {
	outbox := a.store.Events()
	outbox.MaxFailures = cfg.NATS.Defaults().PoisonAttempts
	outbox.OnDead = func(code string) { a.telemetry.Logger.Warn("event quarantined", "code", code) }
	return outbox
}

// WithEvents owns the foreground production lifecycle around the existing UI
// and Actor. Setup remains available without a provider/broker; work ingress
// opens only after broker, Sidecar handshake and authoritative recovery scan.
func (a *App) WithEvents(ctx context.Context, cfg config.Config, run func(context.Context) error) error {
	if ctx.Err() != nil {
		return nil
	}
	a.eventsRequired.Store(true)
	a.runtimeWake = make(chan struct{}, 1)
	workCtx, cancelWork := context.WithCancel(ctx)
	a.runtimeDone = workCtx.Done()
	defer cancelWork()
	pluginDone := make(chan struct{})
	go func() {
		defer close(pluginDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if _, err := a.WorkPlugins(workCtx); err != nil && workCtx.Err() == nil {
					a.telemetry.Logger.Warn("plugin events deferred", "code", "PLUGIN_EVENTS_UNAVAILABLE")
				}
			}
		}
	}()
	defer func() { cancelWork(); <-pluginDone }()
	var watches sync.WaitGroup
	for _, watch := range a.githubWatches {
		watches.Go(func() { watch.Run(workCtx) })
	}
	ctx, cancelInfra := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelInfra()
	type connection struct {
		bus *eventnats.Bus
		sub event.Subscription
	}
	outbox := a.configuredOutbox(cfg)
	connect := func() (connection, error) {
		d, e := a.providerDiagnostics(ctx)
		if e != nil {
			return connection{}, e
		}
		if !d.AgentIngressEnabled {
			return connection{}, agent.ErrSetup
		}
		bus, e := eventnats.OpenWithSecrets(ctx, cfg.NATS, a.control.Secrets)
		if e != nil {
			return connection{}, e
		}
		sub, e := bus.Subscribe(ctx, "runtime_wake_v1")
		if e != nil {
			bus.Close()
			return connection{}, e
		}
		fail := func(e error) (connection, error) { sub.Close(); bus.Close(); return connection{}, e }
		a.telemetry.Logger.Info("startup stage", "stage", "broker_ready")
		if e = a.prepareCore(ctx); e != nil {
			return fail(e)
		}
		if cfg.JavaScript != nil {
			runtime := &codemode.Sidecar{Command: cfg.JavaScript.Command, Script: cfg.JavaScript.Script}
			if e = runtime.Check(ctx); e != nil {
				return fail(e)
			}
		}
		a.telemetry.Logger.Info("startup stage", "stage", "tools_ready")
		if _, e = (event.Publisher{Outbox: outbox, Bus: bus}).Flush(ctx, 100); e != nil {
			return fail(e)
		}
		a.telemetry.Logger.Info("startup stage", "stage", "outboxes_started")
		if e = a.runtimeRuns().Recover(ctx); e != nil {
			return fail(e)
		}
		a.StartMemory(workCtx)
		a.telemetry.Logger.Info("runtime recovery ready", "stage", "recovery_complete")
		a.telemetry.Logger.Info("startup stage", "stage", "ingress_open")
		a.eventsReady.Store(true)
		return connection{bus, sub}, nil
	}
	var current connection
	done := make(chan error, 1)
	go func() {
		var result error
		stopMemory := func() {
			if a.memoryCancel != nil {
				a.memoryCancel()
				<-a.memoryDone
			}
		}
		closeConnection := func() {
			a.eventsReady.Store(false)
			if current.sub != nil {
				current.sub.Close()
				current.bus.Close()
				current = connection{}
			}
		}
		defer func() { stopMemory(); closeConnection(); done <- result }()
		wait := func() bool {
			timer := time.NewTimer(cfg.NATS.RetryInterval)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			case <-a.runtimeWake:
				return true
			}
		}
		for ctx.Err() == nil {
			if current.bus == nil {
				var e error
				current, e = connect()
				if e != nil {
					a.telemetry.Logger.Warn("runtime preparation deferred", "code", "RUNTIME_NOT_READY")
					if ctx.Err() != nil {
						break
					}
					if !wait() {
						break
					}
					continue
				}
			}
			_, e := (event.Publisher{Outbox: outbox, Bus: current.bus}).Flush(ctx, 100)
			if e == nil {
				poll, stop := context.WithTimeout(ctx, 250*time.Millisecond)
				var d event.Delivery
				d, e = current.sub.Next(poll)
				stop()
				if e == nil {
					_, e = outbox.Handle(ctx, "runtime_wake_v1", d, func(context.Context, *sql.Tx, event.CloudEvent) error { return nil })
				}
			}
			if e != nil && ctx.Err() == nil {
				if errors.Is(e, context.DeadlineExceeded) {
					continue
				}
				if errors.Is(e, event.ErrPoison) {
					a.telemetry.Logger.Warn("event poison isolated", "code", "EVENT_POISON")
					continue
				}
				if !errors.Is(e, event.ErrUnavailable) {
					result = e
					cancelWork()
					return
				}
				a.telemetry.Logger.Warn("broker unavailable; durable intents retained", "code", "BROKER_UNAVAILABLE")
				closeConnection()
				if !wait() {
					break
				}
			}
		}
		// New ingress and owned executions have joined before cancellation here.
		// Flush internal wakeups only; never attempt Tool/Delivery effects.
		stopMemory()
		if current.bus != nil {
			drain, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = (event.Publisher{Outbox: outbox, Bus: current.bus}).Flush(drain, 100)
			stop()
		}
	}()
	a.telemetry.Logger.Info("startup stage", "stage", "surface_open")
	err := run(workCtx)
	a.telemetry.Logger.Info("shutdown stage", "stage", "ingress_closed")
	cancelWork()
	watches.Wait()
	cancelInfra()
	infraErr := <-done
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, infraErr)
}
