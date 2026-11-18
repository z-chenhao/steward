// Package v1 holds the original connection intent and account state policies.
package v1

import (
	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

func Connect(rows []contract.ConnectRow, alias string) contract.ConnectPlan {
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

func Check(request contract.AccountCheckRequest) contract.AccountCheckPlan {
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
