package web

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
)

type logoTransport func(*http.Request) (*http.Response, error)

func (f logoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAppLogosBundledForEverySuggestedApp(t *testing.T) {
	s := languageServer()
	editorial, err := (&connectedapps.Service{}).Editorial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range editorial.Items {
		t.Run(app.Slug, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, languageRequest("GET", "/apps/logo/"+app.Slug, "en", ""))
			if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" || w.Header().Get("ETag") == "" {
				t.Fatal(w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, "<path") || strings.Contains(body, "<script") {
				t.Fatal("missing or active logo")
			}
			decoder := xml.NewDecoder(strings.NewReader(body))
			for {
				_, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(app.Slug, err)
				}
			}
		})
	}
}
func TestAppLogoNewProviderMetadataUsesAnonymousBoundedCache(t *testing.T) {
	calls := 0
	cache := appLogoCache{client: &http.Client{Transport: logoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://assets.composio.dev/logos/new_app.svg" || r.Header.Get("x-api-key") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("logo boundary", r.URL, r.Header)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<svg viewBox="0 0 10 10"><path fill="#ea4335" d="M0 0h10v10H0z"/></svg>`)), Header: make(http.Header)}, nil
	})}}
	cache.registerForRevision("", "new_app", "https://assets.composio.dev/logos/new_app.svg")
	for range 2 {
		body, kind, err := cache.getLegacyV1(context.Background(), "new_app")
		if err != nil || kind != "image/svg+xml" || !strings.Contains(string(body), "#ea4335") {
			t.Fatal(kind, err)
		}
	}
	if calls != 1 {
		t.Fatal("logo was not cached", calls)
	}
	for _, raw := range []string{"https://attacker.example/icon.svg", "https://logos.composio.dev.attacker.example/api/foo", "http://logos.composio.dev/api/foo", "https://user@logos.composio.dev/api/foo", "https://logos.composio.dev:8443/api/foo", "https://logos.composio.dev/api/foo?token=private"} {
		if source := logoSource("foo", raw); source != "https://logos.composio.dev/api/foo" {
			t.Fatal(source)
		}
	}
	if _, _, err := cache.getLegacyV1(context.Background(), "unregistered"); err == nil || calls != 1 {
		t.Fatal("unknown logo fetched")
	}
}
func TestAppLogoSanitizationRetainsBrandShapesAndRejectsActiveContent(t *testing.T) {
	body, err := cleanAppSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script><foreignObject><div>remote</div></foreignObject><defs><linearGradient id="brand"><stop offset="0" stop-color="#ff0000"/></linearGradient></defs><path fill="red" style="fill:url(#brand);stroke:url(https://evil.test/a)" d="M0 0h10v10z"/><use href="https://evil.test/a"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"onload", "<script", "alert(", "foreignObject", "evil.test"} {
		if strings.Contains(string(body), bad) {
			t.Fatal("active content retained", string(body))
		}
	}
	if !strings.Contains(string(body), `fill="url(#brand)"`) || !strings.Contains(string(body), "linearGradient") {
		t.Fatal("brand artwork lost", string(body))
	}
	for _, raw := range []string{"<html/>", "<svg/><svg/>", "text<svg/>", "<svg>", strings.Repeat("<g>", 65)} {
		if _, err := cleanAppSVG([]byte(raw)); err == nil {
			t.Fatal("invalid document accepted", raw)
		}
	}
	for _, test := range []struct {
		status int
		body   string
	}{{302, ""}, {200, strings.Repeat("x", maxAppLogo+1)}, {200, "not an image"}} {
		cache := appLogoCache{client: &http.Client{Transport: logoTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
		})}}
		cache.registerForRevision("", "new_app", "")
		if _, _, err := cache.getLegacyV1(context.Background(), "new_app"); err == nil {
			t.Fatal("invalid upstream logo accepted", test.status)
		}
	}
}
