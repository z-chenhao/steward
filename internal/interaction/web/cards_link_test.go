package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type cardsLinkCore struct {
	cardsCore
	destination string
	err         error
	calls       int
	ref         string
	item        int
}

func (c *cardsLinkCore) WebCardsLink(_ context.Context, ref string, item int) (string, error) {
	c.calls++
	c.ref = ref
	c.item = item
	return c.destination, c.err
}
func TestUICardsLinkRouteOriginalDestinationAndGuards(t *testing.T) {
	for _, tc := range []struct {
		query         string
		token         bool
		destination   string
		err           error
		status, calls int
	}{
		{"ref=ui:original%231&item=0", false, "https://example.org/original", nil, 401, 0},
		{"ref=ui:original%231&item=0", true, "https://example.org/original?x=1", nil, 302, 1},
		{"ref=ui:original%231&item=0&url=https://evil.test", true, "https://example.org", nil, 400, 0},
		{"ref=ui:original%231&item=00", true, "https://example.org", nil, 400, 0},
		{"ref=ui:original%231&item=0&item=1", true, "https://example.org", nil, 400, 0},
		{"ref=ui:original%231&item=16", true, "https://example.org", nil, 400, 0},
		{"ref=ui:original%231&item=0", true, "javascript:alert(1)", nil, 404, 1},
		{"ref=ui:original%231&item=0", true, "https://user:secret@example.org", nil, 404, 1},
		{"ref=ui:original%231&item=0", true, "https://example.org", errors.New("PRIVATE_DETAILS"), 404, 1},
	} {
		core := &cardsLinkCore{destination: tc.destination, err: tc.err}
		server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/cards-link?"+tc.query, nil)
		if tc.token {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.status || core.calls != tc.calls || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE_DETAILS") {
			t.Fatal(tc, w.Code, core.calls, w.Body.String())
		}
		if w.Code == 302 && (w.Header().Get("Location") != tc.destination || w.Header().Get("Referrer-Policy") != "no-referrer" || core.ref != "ui:original#1" || core.item != 0) {
			t.Fatal("destination binding changed", w.Header(), core)
		}
	}
}
