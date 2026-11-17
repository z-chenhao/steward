package connectedapps

import (
	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

// Exact pre-service/default23/direct connect-intent interpretation. Existing
// native account workflow consumers and in-flight pre-service reconnects remain
// explicit until default composition
// and retained consumers migrate. Never used after a managed read failure.
// Delete when these consumers no longer require the original interpreter.
func connectIntentLegacyV1(rows []contract.ConnectRow, alias string) contract.ConnectPlan {
	if alias == "" {
		for _, row := range rows {
			if row.Facts&contract.Active != 0 && row.Facts&contract.Public == 0 {
				return contract.ConnectPlan{Kind: "alias_required"}
			}
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].Facts&contract.Pending != 0 {
				row := i
				return contract.ConnectPlan{Kind: "pending", Row: &row}
			}
		}
		alias = "personal"
		for _, row := range rows {
			if row.Alias == alias {
				alias = "personal_" + uuid.NewString()[:8]
			}
		}
	}
	for i, row := range rows {
		if row.Alias == alias && row.Facts&contract.Disconnected == 0 {
			if row.Facts&contract.Pending != 0 {
				value := i
				return contract.ConnectPlan{Kind: "pending", Row: &value}
			}
			return contract.ConnectPlan{Kind: "conflict"}
		}
	}
	return contract.ConnectPlan{Kind: "new", Alias: alias}
}
