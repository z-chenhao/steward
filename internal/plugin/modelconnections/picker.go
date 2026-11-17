package modelconnections

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

//go:embed view/picker-copy.json
var pickerLexicon []byte

func ProjectPicker() contract.PickerPresentation {
	var entries []struct{ Key, Value string }
	if err := json.Unmarshal(pickerLexicon, &entries); err != nil {
		panic(err)
	}
	translated := map[string]string{}
	for _, entry := range entries {
		translated[entry.Key] = entry.Value
	}
	project := func(chinese bool) contract.PickerView {
		t := func(key string) string {
			if chinese {
				if text, ok := translated[key]; ok {
					return text
				}
			}
			return key
		}
		out := contract.PickerView{Label: t("Model"), PlaceholderLabel: t("Choose model"), States: []contract.PickerState{}}
		errors := []string{"", "Some account models are unavailable. Try again.", "The account model list is unavailable.", "Model selection failed. Try again."}
		for outcome, message := range errors {
			for flags := 0; flags < 32; flags++ {
				ready, loading, saving, sending, current := flags&1 != 0, flags&2 != 0, flags&4 != 0, flags&8 != 0, flags&16 != 0
				row := contract.PickerState{ID: flags + outcome*32, Busy: saving || loading, Disabled: saving || sending, Placeholder: !current, Error: t(message)}
				if !ready {
					row.PendingText = t("Loading models…")
				}
				out.States = append(out.States, row)
			}
		}
		return out
	}
	return contract.PickerPresentation{English: project(false), Chinese: project(true)}
}
