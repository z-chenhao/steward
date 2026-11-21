package app

import (
	"context"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (a *App) Schedule(ctx context.Context, ref string) (task.ScheduledWork, error) {
	return a.store.TaskReader().ScheduledDetail(ctx, ref)
}
func (a *App) ScheduleDefaults(ctx context.Context) (automation.EditorDefaults, error) {
	return a.runtimeRuns().OwnerScheduleDefaults(ctx)
}

func (a *App) ConfigureSchedule(ctx context.Context, key string, q automation.OwnerConfiguration) (automation.OwnerReceipt, error) {
	return a.runtimeRuns().ConfigureAutomation(ctx, "web:"+key, q, a.runtimeRuns().AutomationNow())
}

func (a *App) TrialSchedule(ctx context.Context, key, ref string, version int64) (string, error) {
	return a.runtimeRuns().TrialAutomation(ctx, key, ref, version)
}

func (a *App) ManageAutomation(ctx context.Context, key string, q automation.Request) (tool.Result, error) {
	return a.runtimeRuns().ManageAutomation(ctx, key, q, a.runtimeRuns().AutomationNow())
}
func (a *App) ScheduleAutomations(ctx context.Context) error {
	return a.runtimeRuns().ScheduleAutomations(ctx, a.runtimeRuns().AutomationNow())
}
func (a *App) SetTestAutomationClock(now time.Time) {
	a.testAutomationClock = &now
	a.runtimeRuns().SetTestAutomationClock(now)
}

// WorkAutomations is a foreground lifecycle adapter. Reasoning and execution
// remain in Actor.Drain; independent CLI/TUI ingress shares its claims.
func (a *App) WorkAutomations(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := a.Actor(nil).Drain(ctx); err != nil && !errors.Is(err, agent.ErrSetup) {
			if ctx.Err() != nil && errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
