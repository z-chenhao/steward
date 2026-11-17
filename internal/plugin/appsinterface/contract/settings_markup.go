package contract

// Private source contribution for the existing connection-service form.
const SettingsMarkupInterface = "steward.apps-view-markup/5"

type SettingsCopy struct {
	KeyRefLabel string `json:"key_ref_label"`
	AppLabel    string `json:"app_label"`
	ConfigLabel string `json:"config_label"`
	RemoveLabel string `json:"remove_label"`
	AddLabel    string `json:"add_label"`
	SaveLabel   string `json:"save_label"`
}
type SettingsViewSource struct {
	Program string       `json:"program"`
	English SettingsCopy `json:"en"`
	Chinese SettingsCopy `json:"zh-CN"`
}
type SettingsMarkupSource struct {
	ConnectMarkupSource
	Settings SettingsViewSource `json:"settings"`
}
