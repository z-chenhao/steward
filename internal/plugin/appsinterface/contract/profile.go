package contract

const Interface = "steward.apps-interface/1"
const AccountCount = 80
const CardCount = 64

type Account struct {
	Visible           bool     `json:"visible"`
	StatusLabel       string   `json:"status_label"`
	Warning           bool     `json:"warning"`
	Pending           bool     `json:"pending"`
	AuthorizationLink bool     `json:"authorization_link"`
	Actions           []string `json:"actions"`
}
type Card struct {
	Unavailable      bool   `json:"unavailable"`
	FirstLabel       string `json:"first_label"`
	FirstHidden      bool   `json:"first_hidden"`
	Additional       bool   `json:"additional"`
	AdditionalHidden bool   `json:"additional_hidden"`
	EmptyHidden      bool   `json:"empty_hidden"`
	ManageHidden     bool   `json:"manage_hidden"`
	AddHidden        bool   `json:"add_hidden"`
}
type Notice struct {
	Text  string `json:"text"`
	Error bool   `json:"error"`
}
type Browser struct {
	PollNotices                   []Notice `json:"poll_notices"`
	SuccessNotices                []string `json:"success_notices"`
	ActiveFold                    []int    `json:"active_fold"`
	PopupActions                  []string `json:"popup_actions"`
	OpenDetailsAfterAuthorization bool     `json:"open_details_after_authorization"`
	RevealAlias                   bool     `json:"reveal_alias"`
	PollIntervalMS                int      `json:"poll_interval_ms"`
	PollWindowMS                  int      `json:"poll_window_ms"`
	DetailTimeoutMS               int      `json:"detail_timeout_ms"`
	ActionTimeoutMS               int      `json:"action_timeout_ms"`
	Messages                      Messages `json:"messages"`
}
type Messages struct {
	OpenAuthorization   string `json:"open_authorization"`
	DetailsFailed       string `json:"details_failed"`
	ConnectionUnknown   string `json:"connection_unknown"`
	PollPaused          string `json:"poll_paused"`
	PollFailed          string `json:"poll_failed"`
	AuthorizationFailed string `json:"authorization_failed"`
	AuthorizationOpened string `json:"authorization_opened"`
	PopupBlocked        string `json:"popup_blocked"`
	LinkExpired         string `json:"link_expired"`
}
type Notices struct {
	AccountSaved string `json:"account_saved"`
	ServiceSaved string `json:"service_saved"`
	Unsaved      string `json:"unsaved"`
}
type Profile struct {
	Notices  Notices   `json:"notices"`
	Accounts []Account `json:"accounts"`
	Cards    []Card    `json:"cards"`
	Advanced []bool    `json:"advanced"`
	Errors   []string  `json:"errors"`
	Browser  Browser   `json:"browser"`
}

// Version2 owns final display text for the two existing request locales.
const LocalizedInterface = "steward.apps-interface/2"
const LocalizedComposition = "steward.apps-composition/2"

type LocaleRequest struct {
	Locale string `json:"locale"`
}
