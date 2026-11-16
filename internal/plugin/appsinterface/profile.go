package appsinterface

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.apps-interface", Version: "1.21.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.BrowserProfileInterface: compileBrowserProfile, contract.AccountCompositionInterface: compileAccountComposition, contract.CardViewInterface: compileCardView, contract.SidebarViewInterface: compileSidebarView, contract.PagesMarkupInterface: compileAccountMarkup}}
}
func profile() contract.Profile {
	p := contract.Profile{Notices: contract.Notices{AccountSaved: "App settings updated.", ServiceSaved: "App connection service saved.", Unsaved: "Unsaved changes."}, Accounts: make([]contract.Account, contract.AccountCount), Cards: make([]contract.Card, contract.CardCount), Advanced: []bool{false, true, true, true}}
	for i := range p.Accounts {
		p.Accounts[i] = account(i)
	}
	for i := range p.Cards {
		p.Cards[i] = card(i)
	}
	p.Errors = []string{
		"This app requires your own OAuth app. Ask the installation owner to configure it in Composio, then reconnect.",
		"App connections changed. Refresh and try again.",
		"Check the app, account alias and credential reference.",
		"This account cannot be used. Reconnect it to continue.",
		"Choose a label for the additional account.",
		"This authorization link has expired. Reconnect the account to continue.",
		"App service unavailable. Ask the installation owner to check the connection service.",
		"App service unavailable. Ask the installation owner to check the connection service.",
	}
	p.Browser = contract.Browser{PollNotices: []contract.Notice{{}, {Text: "Account connected."}, {}, {Text: "Authorization expired. Reconnect the account to continue.", Error: true}, {}}, SuccessNotices: []string{"App settings updated.", "App settings updated.", "App settings updated.", "App settings updated.", "Account disconnected."}, ActiveFold: []int{0, 1, 2, 1, 1, 1, 2, 2, 2}, PopupActions: []string{"connect", "continue", "reconnect"}, OpenDetailsAfterAuthorization: true, RevealAlias: true, PollIntervalMS: 5000, PollWindowMS: 600000, DetailTimeoutMS: 15000, ActionTimeoutMS: 45000, Messages: contract.Messages{
		OpenAuthorization:   "Open authorization page",
		DetailsFailed:       "App details could not be loaded. Refresh and try again.",
		ConnectionUnknown:   "The connection could not be confirmed. Refresh the page before trying again.",
		PollPaused:          "Automatic status checks paused. Refresh the status or reconnect the account.",
		PollFailed:          "Status could not be refreshed. Your accounts have been kept; retrying shortly.",
		AuthorizationFailed: "The authorization page could not be opened.",
		AuthorizationOpened: "Finish signing in in the authorization tab. This page checks automatically.",
		PopupBlocked:        "The browser blocked the authorization tab. Use Open authorization page to continue.",
		LinkExpired:         "This authorization link has expired. Reconnect the account to continue.",
	}}
	return p
}

// The five real status classes and four original boolean facts make a public
// compilation axis. No actual account/alias/ID/source or credentials are inputs.
func account(i int) contract.Account {
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
func card(i int) contract.Card {
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
