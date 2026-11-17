// Package legacyv1 retains the original never-managed terminal policy only.
package legacyv1

import (
	_ "embed"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
)

//go:embed native-view.json
var original []byte

func View() contract.Copy {
	var out contract.Copy
	if err := json.Unmarshal(original, &out); err != nil {
		panic(err)
	}
	return out
}
func Choose(f contract.ChoiceFacts) contract.Choice {
	if f.Empty && f.Count == 1 {
		return contract.Choice{Kind: "index", Index: 0}
	}
	if f.Exact >= 0 {
		return contract.Choice{Kind: "index", Index: f.Exact}
	}
	if f.Number >= 0 {
		return contract.Choice{Kind: "index", Index: f.Number}
	}
	if f.Manual && !f.Empty {
		return contract.Choice{Kind: "manual"}
	}
	return contract.Choice{Kind: "retry"}
}
