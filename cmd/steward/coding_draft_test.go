package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
)

// The subprocess acceptance binary runs the normal CLI, including its sandbox
// launcher. Only the fixed GitHub origin is routed to the loopback test peer.
type draftPeerTransport struct {
	peer     *url.URL
	original http.RoundTripper
}

func (p draftPeerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" {
		return nil, fmt.Errorf("unexpected fixture origin")
	}
	local := r.Clone(r.Context())
	local.URL.Scheme = p.peer.Scheme
	local.URL.Host = p.peer.Host
	return p.original.RoundTrip(local)
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "-rsi-plugin-exec" {
		main()
		return
	}
	if raw := os.Getenv("STEWARD_TEST_DRAFT_LOOPBACK"); raw != "" {
		peer, err := url.Parse(raw)
		if err != nil || peer.Scheme != "http" || peer.Hostname() != "127.0.0.1" || peer.Port() == "" {
			os.Exit(2)
		}
		http.DefaultTransport = draftPeerTransport{peer, http.DefaultTransport}
		main()
		return
	}
	os.Exit(m.Run())
}
