package webcontent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestWebAccessHTMLDecoderReceivesOriginalBytesAndNeverFallsBack(t *testing.T) {
	original := `<title>source</title><nav>private body</nav><p>A &amp; B</p>`
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, "text/html", original), nil })}
	calls := 0
	sentinel := errors.New("decoder unavailable")
	ctx := WithHTMLParser(context.Background(), func(_ context.Context, data []byte) (string, string, error) {
		calls++
		if string(data) != original {
			t.Fatal("source changed")
		}
		return "", "", sentinel
	})
	if _, err := fetch(ctx, "https://example.com/original", client); !errors.Is(err, sentinel) {
		t.Fatal("fetch fell back", err)
	}
	if _, err := fetchContent(ctx, Config{}, "https://example.com/original", "readable", client); !errors.Is(err, sentinel) {
		t.Fatal("content fell back", err)
	}
	page, err := fetchContent(ctx, Config{}, "https://example.com/original", "raw", client)
	if err != nil || page.Text != original || calls != 2 {
		t.Fatal(page, err, calls)
	}
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, "text/html", string([]byte{255})), nil
	})
	if _, err := fetch(ctx, "https://example.com/original", client); err == nil || err.Error() != "WEB_UNSUPPORTED_ENCODING" || calls != 2 {
		t.Fatal(err, calls)
	}
	// Fixed parser cancellation/failure must not manufacture a successful original page.
	ctx = WithHTMLParser(context.Background(), func(context.Context, []byte) (string, string, error) { return "", "", context.Canceled })
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, "text/html", original), nil })
	if _, err := fetch(ctx, "https://example.com/original", client); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Plain text bypasses the HTML recipe and retains every original byte.
	plain := strings.Repeat("x", 2<<20)
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, "text/plain", plain), nil })
	page, err = fetch(ctx, "https://example.com/original", client)
	if err != nil || page.Text != plain {
		t.Fatal(len(page.Text), err)
	}
}
