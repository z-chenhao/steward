package legacyv1

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

// Original pre-picker service snapshots and explicit never-managed embeddings
// only. No current service failure may use this frozen interpretation.
//
//go:embed picker-v1.json
var pickerV1 []byte

func Picker() contract.PickerPresentation {
	var out contract.PickerPresentation
	if err := json.Unmarshal(pickerV1, &out); err != nil {
		panic(err)
	}
	return out
}
