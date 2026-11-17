package web

import (
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validModelReader(reference string) bool {
	return reference == "" || (len(reference) == 71 && strings.HasPrefix(reference, "sha256:") && strings.Trim(reference[7:], "0123456789abcdef") == "")
}
func validModelPicker(p contract.PickerPresentation) bool {
	text := func(v string, empty bool) bool {
		return (empty || v != "") && len(v) <= 512 && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
	}
	for _, view := range []contract.PickerView{p.English, p.Chinese} {
		if !text(view.Label, false) || !text(view.PlaceholderLabel, false) || len(view.States) != 128 {
			return false
		}
		for i, row := range view.States {
			if row.ID != i || !text(row.PendingText, true) || !text(row.Error, true) {
				return false
			}
		}
	}
	return true
}
func modelPickerView(models composerModels, language string) contract.PickerView {
	if models.Picker == nil {
		return contract.PickerView{}
	}
	if language == "zh-CN" {
		return models.Picker.Chinese
	}
	return models.Picker.English
}
