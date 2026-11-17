package web

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type cardsImageStream struct {
	io.Reader
	closed bool
}

func (s *cardsImageStream) Close() error { s.closed = true; return nil }

type cardsImageCore struct {
	cardsCore
	media  string
	source *cardsImageStream
	calls  int
	ref    string
	item   int
	err    error
}

func (c *cardsImageCore) OpenWebCardsImage(_ context.Context, ref string, item int) (io.ReadCloser, string, error) {
	c.calls++
	c.ref, c.item = ref, item
	if c.err != nil {
		return nil, "", c.err
	}
	return c.source, c.media, nil
}

func TestUICardsImageRouteBindsOriginalItemAndKeepsGuards(t *testing.T) {
	for _, tc := range []struct {
		query         string
		token         bool
		media         string
		err           error
		status, calls int
	}{
		{"ref=ui:original%231&item=0", false, "image/png", nil, 401, 0},
		{"ref=ui:original%231&item=0", true, "image/png", nil, 200, 1},
		{"ref=ui:original%231&item=0&artifact_ref=artifact:other", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=01", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=-1", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=16", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=0&item=1", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=0&bad=%", true, "image/png", nil, 400, 0},
		{"ref=ui:original%231&item=0", true, "image/svg+xml", nil, 415, 1},
		{"ref=ui:original%231&item=0", true, "image/png", errors.New("PRIVATE_DETAILS"), 404, 1},
	} {
		core := &cardsImageCore{media: tc.media, err: tc.err, source: &cardsImageStream{Reader: strings.NewReader("original image bytes")}}
		server := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/cards-image?"+tc.query, nil)
		if tc.token {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != tc.status || core.calls != tc.calls || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE_DETAILS") {
			t.Fatal(tc, w.Code, core.calls, w.Header(), w.Body.String())
		}
		if core.calls > 0 && tc.err == nil && !core.source.closed {
			t.Fatal("image source leaked")
		}
		if w.Code == 200 && (core.ref != "ui:original#1" || core.item != 0 || w.Header().Get("Content-Type") != "image/png" || w.Body.String() != "original image bytes") {
			t.Fatal("image identity changed", core.ref, core.item, w.Body.String())
		}
	}
}
