package contract

// PageMarkupSource is the private page source in the existing Apps package.
// It adds presentation only; no domain State or effect executor crosses this seam.
// Kept experimental until the complete view path and lifecycle are activated.
const PagesMarkupInterface = "steward.apps-view-markup/7"

type PageMarkupSource struct {
	DirectoryMarkupSource
	Pages string `json:"pages"`
}
