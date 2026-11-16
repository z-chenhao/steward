package intelligentui

import (
	"encoding/json"
	"strconv"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// Existing real dates and original ordinal choices are retained. Presentation
// belongs to the same UI package as normalization and answer interpretation.
func meetingView(component int, c Component) (contract.MeetingView, bool) {
	if c.Name != "scheduleTime" || !validNative(c) {
		return contract.MeetingView{}, false
	}
	var props nativeProps
	if json.Unmarshal(c.Props, &props) != nil {
		return contract.MeetingView{}, false
	}
	view := contract.MeetingView{
		Component: component, Title: props.Reason, Slots: []contract.MeetingSlot{},
		DeclineLabel: "None of these work", SelectedTitle: "Time Selected", DeclinedTitle: "No Time Selected",
		SavingLabel: "Saving selection…", SelectError: "Your selection was not confirmed. Retry the same time.",
		DeclineError: "Your selection was not confirmed. Retry the same choice.", SavedLabel: "Time preference saved.",
	}
	duration := strconv.FormatFloat(props.Duration, 'f', -1, 64) + " min"
	for i, slot := range props.TimeSlots {
		view.Slots = append(view.Slots, contract.MeetingSlot{Choice: i, Date: slot.Date, Time: slot.Time, Duration: duration})
	}
	return view, true
}

func meetingViews(components []Component) []contract.MeetingView {
	var views []contract.MeetingView
	for i, c := range components {
		if view, ok := meetingView(i, c); ok {
			views = append(views, view)
		}
	}
	return views
}
