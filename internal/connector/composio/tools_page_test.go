package composio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestClientMethodPageSizeBoundsAndOriginalOpaqueContinuation(t *testing.T) {
	var calls atomic.Int32
	type expectedQuery struct{ limit, cursor string }
	var expected atomic.Pointer[expectedQuery]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		query := request.URL.Query()
		want := expected.Load()
		if want == nil || request.Method != "GET" || request.URL.Path != "/tools" || request.Header.Get("x-api-key") != "project-key-private" || query.Get("toolkit_slug") != "gmail" || query.Get("query") != "exact + & query" || query.Get("cursor") != want.cursor || query.Get("limit") != want.limit || query.Get("include_deprecated") != "false" || query.Get("toolkit_versions") != "latest" || want.limit == "" && query.Has("limit") {
			t.Error("fixed one-page protocol changed original fields", request.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"next_cursor":"unchanged+opaque=&token"}`))
	}))
	defer server.Close()
	client := Client{Endpoint: server.URL, KeyRef: "env:TEST", Secrets: secrets{}}
	for _, size := range []int{1, 4, 8, 1000} {
		expected.Store(&expectedQuery{limit: strconv.Itoa(size)})
		found, err := client.SearchToolsPage(context.Background(), "gmail", "exact + & query", "", size)
		if err != nil || found.NextCursor != "unchanged+opaque=&token" {
			t.Fatal("valid first page changed the provider cursor", size, found, err)
		}
	}
	expected.Store(&expectedQuery{cursor: "unchanged+opaque=&token"})
	if _, err := client.SearchToolsPage(context.Background(), "gmail", "exact + & query", "unchanged+opaque=&token", 0); err != nil {
		t.Fatal("opaque continuation introduced a new page size", err)
	}
	if calls.Load() != 5 {
		t.Fatal("single page was retried", calls.Load())
	}
	for _, item := range []struct {
		cursor string
		limit  int
	}{{"", -1}, {"", 0}, {"", 1001}, {"opaque", 1}, {"opaque", 8}, {"opaque", 1001}} {
		if _, err := client.SearchToolsPage(context.Background(), "gmail", "exact + & query", item.cursor, item.limit); err == nil {
			t.Fatal("invalid page recipe was accepted", item)
		}
	}
	if calls.Load() != 5 {
		t.Fatal("invalid page recipe reached the original HTTP operation", calls.Load())
	}
}
