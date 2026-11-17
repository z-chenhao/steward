package contract

// BrowserProfileInterface explicitly selects the private isolated Apps client.
// It grants no access to the authenticated page or account effects.
const BrowserProfileInterface = "steward.apps-interface/3"

type BrowserView struct {
	Worker string `json:"worker"`
	CSS    string `json:"css"`
}

// Version3 carries its owning client bytes with the already localized facts.
// Legacy versions1/2 retain their explicit historical client interpretation.
type BrowserProfile struct {
	Profile
	View BrowserView `json:"view"`
}
