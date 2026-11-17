package web

import (
	"os"
	"strings"
	"testing"
)

func TestAppsStylesOriginalAndIndependentLayout(t *testing.T) {
	raw, err := os.ReadFile("../../plugin/appsinterface/view/apps.css")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAppStylesheet(string(raw)); err != nil {
		t.Fatal("original stylesheet rejected", err)
	}
	changed := strings.Replace(string(raw), ".app-account{padding:16px 0", ".app-account{padding:24px 0", 1)
	if changed == string(raw) {
		t.Fatal("original layout marker absent")
	}
	if err := validateAppStylesheet(changed); err != nil {
		t.Fatal("independent layout rejected", err)
	}
}
func TestAppsStylesRejectForeignAuthorityAndHiddenDisclosures(t *testing.T) {
	for _, css := range []string{
		`@import url('/secret');`, `.app-account{display:none}`, `.app-account{opacity:0}`, `.app-account{padding:-1px}`, `.app-account strong{font-size:0}`, `.app-account{overflow:hidden;height:1px}`, `.app-detail .muted{color:transparent}`, `.app-account[data-id=private]{padding:8px}`, `body{display:none}`, `.app-logo{background:url('/secret')}`, `.app-logo{background:u/**/rl('/secret')}`, `.app-account{padding:8px!important}`, `.app-account{content:attr(data-private)}`, `@supports(display:grid){.app-account{padding:8px}}`, `.app-account{padding:calc(1px - 8px)}`, `.app-account{padding:var(--private)}`, `.app-logo{position:fixed}`, `.app-account{padding:8px}garbage`,
	} {
		if err := validateAppStylesheet(css); err == nil {
			t.Fatalf("unsafe stylesheet accepted: %s", css)
		}
	}
}
