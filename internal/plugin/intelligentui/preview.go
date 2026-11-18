package intelligentui

import (
	"github.com/z-chenhao/assistant/internal/delivery/uipreview"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

type Preview struct {
	MeetingViews []contract.MeetingView `json:"meeting_views,omitempty"`
	Title        string                 `json:"title"`
	Text         string                 `json:"text,omitempty"`
	Components   []Component            `json:"components"`
}

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
	out.MeetingViews = meetingViews(out.Components)
	return out, true
}
