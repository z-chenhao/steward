package connectedapps

import "sort"

// Exact pre-settings/default23/direct and captured old projection. No managed
// error uses it. Delete when those concrete consumers no longer need it.
func settingsRowsLegacyV1(mode string, rows []SettingsRow, remove int) []SettingsRow {
	result := append([]SettingsRow{}, rows...)
	switch mode {
	case "saved":
		sort.Slice(result, func(i, j int) bool { return result[i].App < result[j].App })
	case "add":
		result = append(result, SettingsRow{})
	case "remove":
		result = append(result[:remove], result[remove+1:]...)
	}
	if len(result) == 0 {
		result = []SettingsRow{{}}
	}
	return result
}
