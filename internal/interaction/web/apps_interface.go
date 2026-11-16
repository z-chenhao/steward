package web

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

type AppInterfaceReader func(context.Context, string, string) (AppInterfacePolicy, error)
type AppInterfacePolicy struct {
	browserView      *contract.BrowserView
	pagesManaged     bool
	pagesMarkup      *template.Template
	directoryManaged bool
	directoryMarkup  *template.Template
	settingsManaged  bool
	settingsMarkup   *appSettingsMarkup
	connectManaged   bool
	connectMarkup    *appConnectMarkup
	accountCopy      *contract.AccountCopy
	accountManaged   bool
	accountMarkup    *template.Template
	graphicsManaged  bool
	markupManaged    bool
	markup           *appMarkup
	sidebarManaged   bool
	sidebarView      *contract.SidebarView
	cardManaged      bool
	cardView         *contract.CardView
	profileLocalized bool
	copyLocalized    bool
	native           bool
	revision         string
	profile          contract.Profile
	composition      *contract.Composition
}

func NativeAppInterface() AppInterfacePolicy { return AppInterfacePolicy{native: true} }
func ManagedAppInterface(revision string, p contract.Profile) (AppInterfacePolicy, error) {
	if revision == "" || validateAppInterface(p) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	return AppInterfacePolicy{revision: revision, profile: p}, nil
}
func (p AppInterfacePolicy) available() bool { return p.native || p.revision != "" }

type appInterfaceKey struct{}

func (s *Server) appInterface(ctx context.Context, reference, locale string) AppInterfacePolicy {
	if p, ok := ctx.Value(appInterfaceKey{}).(AppInterfacePolicy); ok {
		return p
	}
	if s.Interface == nil {
		if reference != "" {
			return AppInterfacePolicy{}
		}
		return NativeAppInterface()
	}
	p, err := s.Interface(ctx, reference, locale)
	if err != nil {
		return AppInterfacePolicy{}
	}
	return p
}
func appStatusClass(status string) int {
	switch status {
	case "active":
		return 1
	case "pending":
		return 2
	case "expired":
		return 3
	case "disconnected":
		return 4
	}
	return 0
}
func appAccountIndex(view *appsView, c connectedapps.Connection) int {
	i := appStatusClass(c.Status) * 16
	if c.Public {
		i |= 8
	}
	if c.RevocationConfirmed {
		i |= 4
	}
	if view.Selected != nil && view.Selected.Slug != c.Toolkit {
		i |= 2
	}
	if view.ConnectionID == c.ID && view.ConnectURL != "" {
		i |= 1
	}
	return i
}
func appCardIndex(view *appsView, c appCard) int {
	i := 0
	if view.Suggested {
		i |= 1
	}
	if c.NoAuth {
		i |= 2
	}
	if c.Connected {
		i |= 4
	}
	if c.PublicActive {
		i |= 8
	}
	if c.Managed {
		i |= 16
	}
	return i
}

type appAccountView struct {
	connectedapps.Connection
	UI contract.Account
}

func appAccountViews(view *appsView) []appAccountView {
	if view == nil {
		return nil
	}
	p := view.Interface
	result := make([]appAccountView, 0, len(view.State.Connections))
	for _, c := range view.State.Connections {
		i := appAccountIndex(view, c)
		var plan contract.Account
		if p == nil || p.native {
			plan = appAccountLegacyV1(i)
		} else if p.available() {
			plan = p.profile.Accounts[i]
		}
		if plan.Visible {
			result = append(result, appAccountView{Connection: c, UI: plan})
		}
	}
	return result
}
func appCardUI(view *appsView, c appCard) contract.Card {
	i := appCardIndex(view, c)
	p := view.Interface
	if p == nil || p.native {
		return appCardLegacyV1(i)
	}
	if !p.available() {
		return contract.Card{Unavailable: true}
	}
	return p.profile.Cards[i]
}
func appAdvanced(view *appsView, notice, errorText string) bool {
	i := 0
	if notice != "" {
		i |= 1
	}
	if errorText != "" {
		i |= 2
	}
	p := view.Interface
	if p == nil || p.native {
		return appAdvancedLegacyV1(i)
	}
	return p.available() && p.profile.Advanced[i]
}
func (p AppInterfacePolicy) notices() contract.Notices {
	if p.native {
		return appNoticesLegacyV1()
	}
	return p.profile.Notices
}
func (p AppInterfacePolicy) errorLabel(err error) string {
	if p.native {
		return appErrorLegacyV1(err)
	}
	if !p.available() {
		return "App presentation is unavailable. The operation could not be confirmed."
	}
	i := 7
	switch {
	case errors.Is(err, connectedapps.ErrAuthConfigRequired):
		i = 0
	case errors.Is(err, connectedapps.ErrConflict):
		i = 1
	case errors.Is(err, connectedapps.ErrInvalid):
		i = 2
	case errors.Is(err, connectedapps.ErrDenied):
		i = 3
	case errors.Is(err, connectedapps.ErrAliasRequired):
		i = 4
	case errors.Is(err, connectedapps.ErrLinkExpired):
		i = 5
	case errors.Is(err, connectedapps.ErrUnavailable):
		i = 6
	}
	return p.profile.Errors[i]
}
func (view *appsView) InterfaceMode() string {
	if view.Interface == nil || view.Interface.native {
		return "native"
	}
	if view.Interface.available() {
		return "managed"
	}
	return "unavailable"
}
func (view *appsView) InterfaceRevision() string {
	if view.Interface == nil {
		return ""
	}
	return view.Interface.revision
}
func (view *appsView) InterfaceJSON() string {
	if view.Interface == nil || view.Interface.native || !view.Interface.available() {
		return ""
	}
	raw, _ := json.Marshal(struct {
		contract.Profile
		BrowserView bool `json:"browser_view,omitempty"`
	}{view.Interface.profile, view.Interface.browserView != nil})
	return string(raw)
}

// These checks preserve original disclosure scope, required uncertainty and
// original-source URL/cache authority. They do not choose labels or control order.
func validateAppInterface(p contract.Profile) error {
	bad := connectedapps.ErrUnavailable
	if len(p.Accounts) != 80 || len(p.Cards) != 64 || len(p.Advanced) != 4 || len(p.Errors) != 8 {
		return bad
	}
	for _, text := range []string{p.Notices.AccountSaved, p.Notices.ServiceSaved, p.Notices.Unsaved} {
		if text == "" || utf8.RuneCountInString(text) > 512 {
			return bad
		}
	}
	for i, row := range p.Accounts {
		status := i / 16
		outside := i&2 != 0
		confirmed := i&4 != 0
		if row.Visible != (!outside && !(status == 4 && confirmed)) || row.Warning != (status == 4 && !confirmed) || row.Pending && status != 2 || row.AuthorizationLink && (status != 2 || i&1 == 0) || row.StatusLabel == "" || utf8.RuneCountInString(row.StatusLabel) > 512 || len(row.Actions) > 3 {
			return bad
		}
		seen := map[string]bool{}
		for _, a := range row.Actions {
			if (a != "reconnect" && a != "check" && a != "disconnect") || seen[a] {
				return bad
			}
			seen[a] = true
		}
		if row.Visible && status != 4 && !seen["disconnect"] {
			return bad
		}
	}
	for _, card := range p.Cards {
		if card.FirstLabel == "" || utf8.RuneCountInString(card.FirstLabel) > 512 {
			return bad
		}
	}
	for _, label := range p.Errors {
		if label == "" || utf8.RuneCountInString(label) > 512 {
			return bad
		}
	}
	b := p.Browser
	if len(b.PollNotices) != 5 || len(b.SuccessNotices) != 5 || len(b.ActiveFold) != 9 || b.PollIntervalMS < 5000 || b.PollIntervalMS > 60000 || b.PollWindowMS < 5000 || b.PollWindowMS > 600000 || b.DetailTimeoutMS < 1 || b.DetailTimeoutMS > 15000 || b.ActionTimeoutMS < 1 || b.ActionTimeoutMS > 45000 {
		return bad
	}
	for i, v := range b.ActiveFold {
		if v < 0 || v > 2 || v != 0 && v != i/3 && v != i%3 {
			return bad
		}
	}
	for _, n := range b.PollNotices {
		if utf8.RuneCountInString(n.Text) > 512 {
			return bad
		}
	}
	for _, text := range b.SuccessNotices {
		if text == "" || utf8.RuneCountInString(text) > 512 {
			return bad
		}
	}
	seen := map[string]bool{}
	for _, a := range b.PopupActions {
		if a != "connect" && a != "continue" && a != "reconnect" || seen[a] {
			return bad
		}
		seen[a] = true
	}
	if len(b.PopupActions) > 3 {
		return bad
	}
	for _, text := range []string{b.Messages.OpenAuthorization, b.Messages.DetailsFailed, b.Messages.ConnectionUnknown, b.Messages.PollPaused, b.Messages.PollFailed, b.Messages.AuthorizationFailed, b.Messages.AuthorizationOpened, b.Messages.PopupBlocked, b.Messages.LinkExpired} {
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 512 {
			return bad
		}
	}

	return nil
}
