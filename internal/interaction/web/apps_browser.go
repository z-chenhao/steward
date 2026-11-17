package web

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

// ManagedAppBrowser attaches an already selected package's private program.
// Its passive stylesheet is checked before either the page or CSS route uses it.
func ManagedAppBrowser(p AppInterfacePolicy, v contract.BrowserView) (AppInterfacePolicy, error) {
	if p.native || p.revision == "" || v.Worker == "" || len(v.Worker) > 64<<10 || !utf8.ValidString(v.Worker) || v.CSS == "" || len(v.CSS) > 32<<10 || !utf8.ValidString(v.CSS) || validateAppStylesheet(v.CSS) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	p.browserView = &v
	return p, nil
}

func (s *Server) appBrowser(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	css := q.Get("format") == "css"
	if (len(q) != 1 && !css) || (css && len(q) != 2) || len(q["interface_revision"]) != 1 || q.Get("interface_revision") == "" || (len(q["format"]) > 0 && (!css || len(q["format"]) != 1)) {
		fail(w, 400)
		return
	}
	// The route never reads accounts or accepts an action. The same selected
	// interface/reference controls this read and the original page's actions.
	p := s.appInterface(r.Context(), q.Get("interface_revision"), requestLanguage(r))
	if !p.available() || p.native || p.browserView == nil {
		fail(w, 503)
		return
	}
	if css {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		fmt.Fprint(w, p.browserView.CSS)
		return
	}
	renderAppBrowser(w, r, p.browserView.Worker)
}

func renderAppBrowser(w http.ResponseWriter, r *http.Request, program string) {
	renderIsolatedBrowser(w, r, program, "steward-apps-browser", "Apps", 0)
}
