package contract

// Picker is a private finite projection of the existing composer states. It
// receives no model IDs, current selection, user input, URL or credential.
const Picker = "steward.model-picker/1"

type PickerState struct {
	ID          int    `json:"id"`
	Busy        bool   `json:"busy"`
	Disabled    bool   `json:"disabled"`
	Placeholder bool   `json:"placeholder"`
	PendingText string `json:"pending_text"`
	Error       string `json:"error"`
}
type PickerView struct {
	Label            string        `json:"label"`
	PlaceholderLabel string        `json:"placeholder_label"`
	States           []PickerState `json:"states"`
}
type PickerPresentation struct {
	English PickerView `json:"en"`
	Chinese PickerView `json:"zh_cn"`
}
