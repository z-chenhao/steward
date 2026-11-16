package web

import "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"

// Only native and declared composition /1-/2 consumers. Remove when those
// original graphs, in-flight actions and published references have retired.
func appAccountCopyLegacyV2(language string) contract.AccountCopy {
	return contract.AccountCopy{
		Reconnect:             translate(language, "Reconnect"),
		RefreshStatus:         translate(language, "Refresh status"),
		Disconnect:            translate(language, "Disconnect"),
		ConfirmDisconnect:     translate(language, "Disconnect this account?"),
		DisconnectDescription: translate(language, "Ghost will stop using this account. Your other accounts stay connected."),
		OpenAuthorization:     translate(language, "Open authorization page"),
	}
}

// Original label helper used only by the pre-markup-/3 template.
func appControlLabel(action string) string {
	switch action {
	case "reconnect":
		return "Reconnect"
	case "check":
		return "Refresh status"
	case "disconnect":
		return "Disconnect"
	}
	return ""
}
