package web

import (
	"errors"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

// Exact pre-interface/default23/direct and captured native requests only.
// Managed missing/invalid profiles never call these historical policies.
// Delete when those concrete consumers and references retire.
func appErrorLegacyV1(err error) string {
	if errors.Is(err, connectedapps.ErrAuthConfigRequired) {
		return "This app requires your own OAuth app. Ask the installation owner to configure it in Composio, then reconnect."
	}
	switch {
	case errors.Is(err, connectedapps.ErrConflict):
		return "App connections changed. Refresh and try again."
	case errors.Is(err, connectedapps.ErrInvalid):
		return "Check the app, account alias and credential reference."
	case errors.Is(err, connectedapps.ErrDenied):
		return "This account cannot be used. Reconnect it to continue."
	case errors.Is(err, connectedapps.ErrAliasRequired):
		return "Choose a label for the additional account."
	case errors.Is(err, connectedapps.ErrLinkExpired):
		return "This authorization link has expired. Reconnect the account to continue."
	default:
		return "App service unavailable. Ask the installation owner to check the connection service."
	}
}
func appAccountLegacyV1(i int) contract.Account {
	status := i / 16
	public := i&8 != 0
	confirmed := i&4 != 0
	outside := i&2 != 0
	link := i&1 != 0
	p := contract.Account{Visible: !outside && !(status == 4 && confirmed), StatusLabel: "Disconnected", Warning: status == 4 && !confirmed, Pending: status == 2, AuthorizationLink: status == 2 && link, Actions: []string{}}
	if public {
		if status == 1 {
			p.StatusLabel = "Enabled public service"
		}
	} else {
		switch status {
		case 1:
			p.StatusLabel = "Connected"
		case 2:
			p.StatusLabel = "Awaiting authorization"
		case 3:
			p.StatusLabel = "Authorization expired"
		}
	}
	if status != 4 {
		if status != 1 && !public {
			p.Actions = append(p.Actions, "reconnect")
		}
		p.Actions = append(p.Actions, "check", "disconnect")
	}
	return p
}
func appCardLegacyV1(i int) contract.Card {
	suggested := i&1 != 0
	noAuth := i&2 != 0
	active := i&4 != 0
	public := i&8 != 0
	managed := i&16 != 0
	alias := i&32 != 0
	label := "Connect"
	if noAuth {
		label = "Enable app"
	}
	return contract.Card{Unavailable: suggested, FirstLabel: label, FirstHidden: active || alias, Additional: !suggested, AdditionalHidden: (!active && !alias) || public, EmptyHidden: managed, ManageHidden: !managed, AddHidden: managed}
}

func appNoticesLegacyV1() contract.Notices {
	return contract.Notices{AccountSaved: "App settings updated.", ServiceSaved: "App connection service saved.", Unsaved: "Unsaved changes."}
}

func appAdvancedLegacyV1(facts int) bool { return facts != 0 }
