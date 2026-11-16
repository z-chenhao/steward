package builtin

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
)

//go:embed capability-forms-owner-v1.json
var capabilityFormsOwner []byte

//go:embed capability-presentation-owner-v1.json
var capabilityPresentationOwner []byte

func capabilityFormCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(capabilityFormsOwner, &cases)
	if err != nil {
		return nil, err
	}
	var presentation rsi.Case
	if err = json.Unmarshal(capabilityPresentationOwner, &presentation); err != nil {
		return nil, err
	}
	return append(cases, presentation), nil
}
