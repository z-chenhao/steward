package webcontent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWebAccessManagedRoutePreservesRawBytesAndSourceFacts(t *testing.T) {
	body := []byte("<title>原始</title><article>exact &amp; evidence</article>\n")
	page := Page{URL: "https://example.com/original", MediaType: "text/html", RetrievedAt: "2026-11-09T00:00:00Z", Freshness: "original origin", Untrusted: true}
	calls := 0
	ctx := WithRouteSelector(context.Background(), func(_ context.Context, q RouteRequest) (RoutePlan, error) {
		calls++
		if q.Operation != "content" || q.MediaType != "text/html" || q.Mode != "raw" || q.DocumentAvailable {
			t.Fatal(q)
		}
		return RoutePlan{Decoder: "text"}, nil
	})
	got, err := DecodePage(ctx, Config{}, page, "content", "raw", body)
	if err != nil || got.Text != string(body) || got.URL != page.URL || got.MediaType != page.MediaType || got.RetrievedAt != page.RetrievedAt || got.Freshness != page.Freshness || !got.Untrusted || calls != 1 {
		t.Fatal(got, calls, err)
	}
}

func TestWebAccessManagedRouteRejectsIncompatibleCodecWithoutLegacyFallback(t *testing.T) {
	for _, row := range []struct {
		name, kind, operation, mode, decoder string
	}{
		{"raw cannot parse HTML", "text/html", "content", "raw", "html"},
		{"image cannot become text", "image/png", "content", "readable", "text"},
		{"answer cannot attach image", "image/png", "content", "answer", "image"},
		{"simple fetch cannot parse PDF", "application/pdf", "fetch", "", "document"},
		{"missing resource cannot parse PDF", "application/pdf", "content", "readable", "document"},
		{"text cannot become HTML", "text/plain", "content", "readable", "html"},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx := WithRouteSelector(context.Background(), func(context.Context, RouteRequest) (RoutePlan, error) { return RoutePlan{Decoder: row.decoder}, nil })
			page, err := DecodePage(ctx, Config{}, Page{URL: "https://example.com/source", MediaType: row.kind}, row.operation, row.mode, []byte("original evidence"))
			if !errors.Is(err, ErrRouteUnavailable) || page.Text != "" || page.Image != nil {
				t.Fatal(page, err)
			}
		})
	}
	ctx := WithRouteSelector(context.Background(), func(context.Context, RouteRequest) (RoutePlan, error) {
		return RoutePlan{}, errors.New("candidate stopped")
	})
	if _, err := DecodePage(ctx, Config{}, Page{MediaType: "text/plain"}, "content", "", []byte("legacy could read this")); !errors.Is(err, ErrRouteUnavailable) {
		t.Fatal(err)
	}
	for _, decoder := range []string{"html", "document", "image"} {
		ctx := WithRouteSelector(context.Background(), func(context.Context, RouteRequest) (RoutePlan, error) { return RoutePlan{Decoder: decoder}, nil })
		kind := "text/html"
		if decoder == "image" {
			kind = "image/png"
		}
		_, err := DecodePage(ctx, Config{ExtractCommand: "/absent/node", ExtractScript: "/absent/parser.mjs"}, Page{MediaType: kind}, "content", "", []byte("original body"))
		if !errors.Is(err, ErrRouteUnavailable) {
			t.Fatal("missing managed decoder used a legacy recipe", decoder, err)
		}
	}
}

func TestWebAccessManagedRouteKeepsByteLimitsEncodingAndEmptyFailure(t *testing.T) {
	ctx := WithRouteSelector(context.Background(), func(context.Context, RouteRequest) (RoutePlan, error) { return RoutePlan{Decoder: "text"}, nil })
	page := Page{MediaType: "text/plain"}
	for _, row := range []struct {
		op   string
		data []byte
		code string
	}{
		{"content", []byte(strings.Repeat("x", 8<<20)), ""},
		{"content", []byte(strings.Repeat("x", (8<<20)+1)), "RESOURCE_LIMIT"},
		{"fetch", []byte(strings.Repeat("x", (2<<20)+1)), "WEB_BODY_LIMIT"},
		{"content", []byte{0xff}, "WEB_UNSUPPORTED_ENCODING"},
		{"content", []byte(" \n\t"), "WEB_EMPTY_OR_DYNAMIC_PAGE"},
	} {
		got, err := DecodePage(ctx, Config{}, page, row.op, "", row.data)
		if row.code == "" {
			if err != nil || got.Text != string(row.data) {
				t.Fatal(len(got.Text), err)
			}
		} else if err == nil || err.Error() != row.code {
			t.Fatal(row.code, err)
		}
	}
}
