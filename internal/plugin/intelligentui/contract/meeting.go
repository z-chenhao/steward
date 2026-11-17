package contract

// MeetingView is a private static contribution inside the existing normalized
// document receipt. Original numeric choices remain Host-bound form arguments.
// It has no separate service, lifecycle, resource, or execution environment.
type MeetingView struct {
	Component     int           `json:"component"`
	Title         string        `json:"title"`
	Slots         []MeetingSlot `json:"slots"`
	DeclineLabel  string        `json:"decline_label"`
	SelectedTitle string        `json:"selected_title"`
	DeclinedTitle string        `json:"declined_title"`
	SavingLabel   string        `json:"saving_label"`
	SelectError   string        `json:"select_error"`
	DeclineError  string        `json:"decline_error"`
	SavedLabel    string        `json:"saved_label"`
}
type MeetingSlot struct {
	Choice   int    `json:"choice"`
	Date     string `json:"date"`
	Time     string `json:"time"`
	Duration string `json:"duration"`
}

// MeetingDisplayRequired names the retained pre-1.25 release boundary. New or
// unrecognized versions must provide complete display data for their meetings.
func MeetingDisplayRequired(version string) bool { return displayRequired(version, 25) }
