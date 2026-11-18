package ui

import (
	"context"
	"github.com/z-chenhao/assistant/internal/delivery/uipreview"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// Preview is an ephemeral display, never a delivered artifact or an authority to
// submit an answer. Executable fields and artifact/action references are absent
// from the wire. Private Table Workers run only in the read-only opaque driver.
type Preview struct {
	MeetingViews   map[int]contract.MeetingView `json:"meeting_views,omitempty"`
	MeetingManaged bool                         `json:"meeting_view,omitempty"`
	FormViews      map[int]FormView             `json:"form_views,omitempty"`
	FormManaged    bool                         `json:"form_view,omitempty"`
	ChartViews     map[int]ChartView            `json:"-"`
	ChartManaged   bool                         `json:"chart_view,omitempty"`
	TableViews     map[int]TableView            `json:"-"`
	TableManaged   bool                         `json:"table_view,omitempty"`
	CardsViews     map[int]CardsView            `json:"-"`
	CardsManaged   bool                         `json:"cards_view,omitempty"`
	PlanViews      map[int]PlanView             `json:"-"`
	PlanManaged    bool                         `json:"plan_view,omitempty"`
	Order          string                       `json:"-"`
	Title          string                       `json:"title"`
	Text           string                       `json:"text,omitempty"`
	Components     []Component                  `json:"components"`
}

// StagedPreview carries private recovery metadata, never a model tool call.
type StagedPreview struct {
	Key, Arguments, Order string
}

type previewObserverKey struct{}
type PreviewObserver func(string, string, string)

func WithPreviewObserver(ctx context.Context, observe PreviewObserver) context.Context {
	return context.WithValue(ctx, previewObserverKey{}, observe)
}

// ObservePreview is called outside the staging transaction. The ordinary tool
// completion, reply binding and Delivery remain the only durable authorities.
func ObservePreview(ctx context.Context, key, raw string, order ...string) {
	if observe, ok := ctx.Value(previewObserverKey{}).(PreviewObserver); ok && ctx.Err() == nil {
		position := ""
		if len(order) > 0 {
			position = order[0]
		}
		observe(key, raw, position)
	}
}

// ParsePreview accepts a bounded JSON prefix. Only complete array members are
// shown, except the current components/props object and CSS/HTML string prefix.
// Every native component still passes the owning catalog's validation.
func ParsePreview(raw string) (Preview, bool) {
	draft, ok := uipreview.ParseV1(raw)
	if !ok {
		return Preview{}, false
	}
	out := Preview{Title: draft.Title, Text: draft.Text}
	for _, c := range draft.Components {
		component := Component{Name: c.Name, Props: c.Props}
		if component.Name == "generateSandboxedUi" || validNative(component) {
			out.Components = append(out.Components, component)
		}
	}
	return out, true
}
