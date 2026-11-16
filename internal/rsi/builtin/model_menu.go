package builtin

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Expected outputs were captured by executing the original native menu, not
// obtained by calling candidate code during verification.
//
//go:embed model-menu-owner-v1.json
var modelMenuOwner []byte

//go:embed model-picker-owner-v1.json
var modelPickerOwner []byte

func modelMenuCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(modelMenuOwner, &cases)
	if err != nil {
		return nil, err
	}
	var picker rsi.Case
	if err = json.Unmarshal(modelPickerOwner, &picker); err != nil {
		return nil, err
	}
	return append(cases, picker), nil
}
