package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// Only the current fenced loop publishes previews. They never enter Trace,
// GenerationResult, Checkpoint, Conversation or Delivery.
func (l *agentLoop) beginUIPreview(r Run) {
	if r.Kind != MainRole || l.uiSession != "" || l.execution.Err() != nil {
		return
	}
	l.uiSession = uuid.NewString()
	session := l.uiSession
	event := l.event(r, "ui_preview_start", "", "")
	event.PreviewID = session
	l.publish(event)
	execution := l.execution
	discarded := event
	discarded.Kind = "ui_preview_discarded"
	l.uiBase = execution
	l.uiStop = context.AfterFunc(execution, func() { l.publish(discarded) })
	observe := func(key, raw, order string) {
		if raw == "" && execution.Err() == nil {
			e := event
			e.Kind = "ui_preview_remove"
			e.PreviewKey = key
			l.publish(e)
			return
		}
		preview, ok := ui.ParsePreview(raw)
		if !ok || execution.Err() != nil {
			return
		}
		e := event
		e.Kind = "ui_preview_update"
		e.PreviewKey = key
		preview.Order = order
		e.UIPreview = &preview
		l.publish(e)
	}
	l.execution = ui.WithPreviewObserver(l.execution, observe)
	// Resuming a waiting loop reattaches already staged successful calls. This
	// is a read under the Run claim, never a second execution or a GET mutation.
	if store, ok := l.options.Store.(interface {
		StagedUIPreviews(context.Context, RunClaim) ([]ui.StagedPreview, error)
	}); ok {
		if calls, err := store.StagedUIPreviews(l.execution, l.claim); err == nil {
			for _, call := range calls {
				observe(call.Key, call.Arguments, call.Order)
			}
		}
	}
}
func (l *agentLoop) publishUIPreview(r Run, kind, key string, preview *ui.Preview) {
	if r.Kind != MainRole || l.uiSession == "" {
		return
	}
	e := l.event(r, kind, "", "")
	e.PreviewID = l.uiSession
	e.PreviewKey = key
	e.UIPreview = preview
	l.publish(e)
}
func (l *agentLoop) endUIPreview(r Run, kind, generation string) {
	if l.uiSession == "" {
		return
	}
	e := l.event(r, kind, "", "")
	e.PreviewID = l.uiSession
	e.GenerationID = generation
	l.uiSession = ""
	l.uiStop()
	l.uiStop = nil
	l.execution = l.uiBase
	l.uiBase = nil
	l.publish(e)
}
