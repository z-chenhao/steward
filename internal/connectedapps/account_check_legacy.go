package connectedapps

import "github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"

// Exact pre-check/default23/direct and captured pre-opt-in source interpretation.
// No managed failure uses it. Delete with those explicit remaining consumers.
func accountCheckLegacyV1(request contract.AccountCheckRequest) contract.AccountCheckPlan {
	if request.Public {
		return contract.AccountCheckPlan{Status: "refresh"}
	}
	status := "expired"
	switch {
	case request.Disabled:
	case request.Active:
		status = "active"
	case request.Initializing || request.Initiated:
		status = "pending"
	}
	return contract.AccountCheckPlan{Status: status}
}
