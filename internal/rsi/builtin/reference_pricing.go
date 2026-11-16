package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/rsi"
)

// Captured from the original native Quote, never from candidate execution.
//
//go:embed reference-pricing-owner-v1.json
var referencePricingOwner []byte

func referencePricingCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(referencePricingOwner, &cases)
	return cases, err
}
