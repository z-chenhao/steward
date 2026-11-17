// Package legacyv1 serves only explicit installations/embeddings which never
// activated Feishu presentation. Delete when those consumers retire; an active
// or stopped managed selection cannot invoke this interpreter.
package legacyv1

import "github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"

func ProjectStatus(f contract.StatusFacts) contract.StatusProjection {
	action := ""
	switch f.State {
	case "credentials_rejected":
		action = "Check the configured application credentials or replace the external access token."
	case "credentials_unavailable":
		action = "Check SecretStore access and connectivity to the Feishu API."
	case "not_configured":
		action = "Configure feishu and bind the exact owner and chat through channel manage."
	case "ready":
		action = ""
	case "unavailable":
		action = "Check connectivity, application activation and bot permissions."
	case "unchecked":
		action = "Run channel check to verify credentials without sending a message."
	}
	return contract.StatusProjection{Action: action}
}
