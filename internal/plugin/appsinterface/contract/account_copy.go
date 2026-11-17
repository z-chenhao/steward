package contract

// AccountComposition adds six ordinary labels to the existing localized view.
// Original action identities, authorization and required revocation disclosures
// remain Host mechanisms, never candidate instructions.
const AccountCompositionInterface = "steward.apps-composition/3"

type AccountCopy struct {
	Reconnect             string `json:"reconnect"`
	RefreshStatus         string `json:"refresh_status"`
	Disconnect            string `json:"disconnect"`
	ConfirmDisconnect     string `json:"confirm_disconnect"`
	DisconnectDescription string `json:"disconnect_description"`
	OpenAuthorization     string `json:"open_authorization"`
}

type AccountComposition struct {
	Composition
	AccountCopy AccountCopy `json:"account_copy"`
}
