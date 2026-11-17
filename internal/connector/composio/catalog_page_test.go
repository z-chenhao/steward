package composio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientCatalogPageKeepsOriginalQueryAndOpaqueContinuation(t *testing.T) {
	var calls atomic.Int32
	const cursor = "opaque+provider/cursor=40"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/toolkits" || q.Get("search") != "original + 汉字" || q.Get("category") != "productivity" || q.Get("type") != "native" || q.Get("sort_by") != "alphabetically" || q.Get("include_deprecated") != "false" || r.Header.Get("x-api-key") != "project-key-private" {
			t.Error("original protocol scope changed", r.Method, r.URL)
			http.Error(w, "invalid scope", 400)
			return
		}
		if call == 1 && q.Get("limit") != "1000" || call == 2 && (q.Get("cursor") != cursor || q.Has("limit")) {
			t.Error("page recipe changed", q)
			http.Error(w, "invalid page", 400)
			return
		}
		item := Toolkit{Slug: "gmail", Name: "Gmail"}
		item.Meta.Description = "project-key-private"
		_ = json.NewEncoder(w).Encode(Catalog{Items: []Toolkit{item}, NextCursor: cursor})
	}))
	defer peer.Close()
	client := Client{Endpoint: peer.URL, KeyRef: "env:TEST", Secrets: secrets{}}
	first, err := client.CatalogPage(context.Background(), "original + 汉字", "productivity", "", 1000)
	if err != nil || first.NextCursor != cursor || len(first.Items) != 1 || strings.Contains(first.Items[0].Meta.Description, "project-key-private") {
		t.Fatal(first, err)
	}
	if _, err := client.CatalogPage(context.Background(), "original + 汉字", "productivity", cursor, 0); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
	for _, bad := range []struct {
		cursor string
		limit  int
	}{{"", 0}, {"", -1}, {"", 1001}, {cursor, 1}, {cursor, 40}} {
		if _, err := client.CatalogPage(context.Background(), "original + 汉字", "productivity", bad.cursor, bad.limit); err == nil || calls.Load() != 2 {
			t.Fatal("invalid page recipe reached original IO", bad, err, calls.Load())
		}
	}
}
