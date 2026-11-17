package sqlite

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func bindAcceptedMeetingViews(widget *ui.Widget, views []contract.MeetingView, version string) error {
	if !contract.MeetingDisplayRequired(version) {
		return nil
	}
	expected := map[int]ui.Component{}
	for i, c := range widget.Components {
		if c.Name == "scheduleTime" {
			expected[i] = c
		}
	}
	if len(views) != len(expected) {
		return agent.ErrStorage
	}
	for _, view := range views {
		component, ok := expected[view.Component]
		if !ok {
			return agent.ErrStorage
		}
		var props struct {
			Slots []struct {
				Date string `json:"date"`
				Time string `json:"time"`
			} `json:"timeSlots"`
		}
		if json.Unmarshal(component.Props, &props) != nil || len(props.Slots) == 0 || len(props.Slots) > 16 || len(view.Slots) != len(props.Slots) {
			return agent.ErrStorage
		}
		for _, text := range []string{view.Title, view.DeclineLabel, view.SelectedTitle, view.DeclinedTitle, view.SavingLabel, view.SelectError, view.DeclineError, view.SavedLabel} {
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len([]rune(text)) < 1 || len([]rune(text)) > 300 {
				return agent.ErrStorage
			}
		}
		seen := map[int]bool{}
		for _, slot := range view.Slots {
			if slot.Choice < 0 || slot.Choice >= len(props.Slots) || seen[slot.Choice] || slot.Date != props.Slots[slot.Choice].Date || slot.Time != props.Slots[slot.Choice].Time || !utf8.ValidString(slot.Duration) || strings.ContainsRune(slot.Duration, 0) || len([]rune(slot.Duration)) > 100 {
				return agent.ErrStorage
			}
			seen[slot.Choice] = true
		}
		widget.Components[view.Component].MeetingManaged = true
		copy := view
		widget.Components[view.Component].MeetingView = &copy
		delete(expected, view.Component)
	}
	return nil
}
