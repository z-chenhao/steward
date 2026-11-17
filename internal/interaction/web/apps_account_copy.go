package web

import (
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func ManagedAppAccountComposition(p AppInterfacePolicy, c contract.AccountComposition) (AppInterfacePolicy, error) {
	for _, text := range []string{c.AccountCopy.Reconnect, c.AccountCopy.RefreshStatus, c.AccountCopy.Disconnect, c.AccountCopy.ConfirmDisconnect, c.AccountCopy.DisconnectDescription, c.AccountCopy.OpenAuthorization} {
		if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 512 {
			return AppInterfacePolicy{}, connectedapps.ErrUnavailable
		}
	}
	p, err := ManagedLocalizedAppComposition(p, c.Composition)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.accountCopy = &c.AccountCopy
	return p, nil
}

func appAccountCopy(view *appsView, language string) contract.AccountCopy {
	if view == nil || view.Interface == nil || view.Interface.native {
		return appAccountCopyLegacyV2(language)
	}
	p := view.Interface
	if !p.available() {
		return contract.AccountCopy{}
	}
	if p.accountCopy != nil {
		return *p.accountCopy
	}
	// Explicit pre-/3 composition graphs retain their original dictionary.
	return appAccountCopyLegacyV2(language)
}

func accountActionLabel(c contract.AccountCopy, action string) string {
	switch action {
	case "reconnect":
		return c.Reconnect
	case "check":
		return c.RefreshStatus
	case "disconnect":
		return c.Disconnect
	}
	return ""
}
