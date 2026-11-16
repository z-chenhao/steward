package appsinterface

import "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"

// Shared localized facts live outside captured legacy producer files, so
// historical compilation never depends on a helper absent from its snapshot.
func localizedProfile(locale string) contract.Profile {
	p := profile()
	for i := range p.Accounts {
		p.Accounts[i].StatusLabel = localizedText(locale, p.Accounts[i].StatusLabel)
	}
	for i := range p.Cards {
		p.Cards[i].FirstLabel = localizedText(locale, p.Cards[i].FirstLabel)
	}
	for i := range p.Errors {
		p.Errors[i] = localizedText(locale, p.Errors[i])
	}
	p.Notices.AccountSaved = localizedText(locale, p.Notices.AccountSaved)
	p.Notices.ServiceSaved = localizedText(locale, p.Notices.ServiceSaved)
	p.Notices.Unsaved = localizedText(locale, p.Notices.Unsaved)
	p.Browser.Messages.OpenAuthorization = localizedText(locale, p.Browser.Messages.OpenAuthorization)
	p.Browser.Messages.DetailsFailed = localizedText(locale, p.Browser.Messages.DetailsFailed)
	p.Browser.Messages.ConnectionUnknown = localizedText(locale, p.Browser.Messages.ConnectionUnknown)
	p.Browser.Messages.PollPaused = localizedText(locale, p.Browser.Messages.PollPaused)
	p.Browser.Messages.PollFailed = localizedText(locale, p.Browser.Messages.PollFailed)
	p.Browser.Messages.AuthorizationFailed = localizedText(locale, p.Browser.Messages.AuthorizationFailed)
	p.Browser.Messages.AuthorizationOpened = localizedText(locale, p.Browser.Messages.AuthorizationOpened)
	p.Browser.Messages.PopupBlocked = localizedText(locale, p.Browser.Messages.PopupBlocked)
	p.Browser.Messages.LinkExpired = localizedText(locale, p.Browser.Messages.LinkExpired)

	for i := range p.Browser.PollNotices {
		p.Browser.PollNotices[i].Text = localizedText(locale, p.Browser.PollNotices[i].Text)
	}
	for i := range p.Browser.SuccessNotices {
		p.Browser.SuccessNotices[i] = localizedText(locale, p.Browser.SuccessNotices[i])
	}
	return p
}
