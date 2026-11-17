package connectedapps

import "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"

// Exact label choices for existing pre-directory/native and explicit
// direct/unbundled consumers. Never selected on managed error or empty selection.
// Delete once these named compositions no longer need the original interpreter.
func labelPolicyLegacyV1() *contract.LabelPolicy {
	return &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "name", UpperInitial: true}, Unnamed: contract.LabelPlan{NameSource: "slug", UpperInitial: true}}
}
