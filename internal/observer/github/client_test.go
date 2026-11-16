package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
)

type peerTransport struct {
	server    *url.URL
	transport http.RoundTripper
}

func (p peerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
		return nil, errors.New("untrusted endpoint")
	}
	clone := r.Clone(r.Context())
	clone.URL.Scheme = p.server.Scheme
	clone.URL.Host = p.server.Host
	return p.transport.RoundTrip(clone)
}

func peerClient(t *testing.T, handler http.HandlerFunc) Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretmemory.New()
	if err = secrets.Put(context.Background(), "env:WATCH_TEST", []byte("synthetic-test-token")); err != nil {
		t.Fatal(err)
	}
	return Client{Secrets: secrets, HTTP: &http.Client{Transport: peerTransport{u, http.DefaultTransport}, Timeout: time.Second}}
}

func testConfig() Config {
	return Config{Repository: "owner/repo", PullNumber: 7, CredentialRef: "env:WATCH_TEST", PollSeconds: 5}
}

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func basePeer(t *testing.T, r *http.Request, w http.ResponseWriter) bool {
	t.Helper()
	if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-test-token" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
		t.Errorf("unexpected authenticated request")
	}
	switch r.URL.Path {
	case "/user":
		fmt.Fprint(w, `{"id":1,"login":"steward-owner"}`)
	case "/repos/owner/repo/pulls/7":
		fmt.Fprintf(w, `{"number":7,"head":{"sha":%q},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`, testSHA)
	default:
		return false
	}
	return true
}

func TestReadExactPRPaginatedUpdatesAndOwnOutput(t *testing.T) {
	var revision atomic.Int64
	var pages atomic.Int64
	client := peerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if basePeer(t, r, w) {
			return
		}
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/7/comments":
			pages.Add(1)
			count := 1
			if r.URL.Query().Get("page") == "1" {
				count = 100
			}
			comments := make([]map[string]any, 0, count)
			for i := 0; i < count; i++ {
				id := int64(i + 1)
				if count == 1 {
					id = 101
				}
				actor := int64(2)
				if id == 1 {
					actor = 1
				}
				comments = append(comments, map[string]any{"id": id, "body": fmt.Sprint("comment-", id, "-revision-", revision.Load()), "updated_at": "2026-11-01T10:00:00Z", "html_url": "https://github.com/owner/repo/pull/7#discussion_r1", "user": map[string]any{"id": actor, "login": "reviewer"}})
			}
			// A poisoned Link is never followed: pages are constructed locally.
			w.Header().Set("Link", `<https://attacker.invalid/token>; rel="next"`)
			json.NewEncoder(w).Encode(comments)
		case "/repos/owner/repo/pulls/7/reviews":
			fmt.Fprint(w, `[{"id":500,"body":"changes needed","state":"CHANGES_REQUESTED","submitted_at":"2026-11-01T10:01:00Z","html_url":"https://github.com/owner/repo/pull/7#pullrequestreview-500","user":{"id":2,"login":"reviewer"}}]`)
		case "/repos/owner/repo/commits/" + testSHA + "/check-runs":
			fmt.Fprintf(w, `{"check_runs":[{"id":600,"name":"Ubuntu checks","status":"completed","conclusion":"success","head_sha":%q,"completed_at":"2026-11-01T10:02:00Z","html_url":"https://github.com/owner/repo/actions/runs/600"}]}`, testSHA)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	batch, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || len(batch.Events) != 102 || pages.Load() != 2 || batch.RepositoryID != 99 {
		t.Fatalf("batch: %d %+v %v", len(batch.Events), batch, err)
	}
	if batch.Events[0].SourceID != 2 || batch.Events[100].Kind != "review" || batch.Events[101].State != "success" {
		t.Fatal("wrong filtering or event kinds")
	}
	repeated, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || repeated.Events[0].Key != batch.Events[0].Key {
		t.Fatal("unstable identity", err)
	}
	revision.Store(1)
	changed, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || changed.Events[0].Key == batch.Events[0].Key {
		t.Fatal("source edit lost", err)
	}
}

func TestReadRejectsPartialHistoryRedirectAndInvalidSource(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        int
		payload, code string
	}{
		{"revoked", 401, "sensitive-provider-response", "CREDENTIAL_REJECTED"},
		{"rate-limited", 429, "sensitive-provider-response", "ACCESS_DENIED_OR_RATE_LIMITED"},
		{"redirect", 302, "", "SOURCE_MOVED"},
		{"null", 200, "null", "INVALID_RESPONSE"},
		{"trailing", 200, "[] {}", "INVALID_RESPONSE"},
		{"wrong-source", 200, `[{"id":1,"body":"bad","updated_at":"2026-11-01T00:00:00Z","html_url":"https://github.com/other/repo/pull/7","user":{"id":2,"login":"a"}}]`, "INVALID_RESPONSE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := peerClient(t, func(w http.ResponseWriter, r *http.Request) {
				if basePeer(t, r, w) {
					return
				}
				w.Header().Set("Location", "https://attacker.invalid/collect")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.payload)
			})
			batch, err := client.readLegacyV1(context.Background(), testConfig())
			var failure *Failure
			if !errors.As(err, &failure) || failure.Code != test.code || len(batch.Events) != 0 || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "synthetic-test-token") {
				t.Fatalf("unsafe result: %+v %v", batch, err)
			}
		})
	}
}

func TestReadReviewIdentityIncludesBodyBeyondDisplayLimit(t *testing.T) {
	var revision atomic.Int64
	client := peerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if basePeer(t, r, w) {
			return
		}
		switch r.URL.Path {
		case "/repos/owner/repo/pulls/7/comments":
			fmt.Fprint(w, `[]`)
		case "/repos/owner/repo/pulls/7/reviews":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 500, "body": strings.Repeat("a", 16<<10) + fmt.Sprint(revision.Load()), "state": "COMMENTED", "submitted_at": "2026-11-01T00:00:00Z", "html_url": "https://github.com/owner/repo/pull/7#pullrequestreview-500", "user": map[string]any{"id": 2, "login": "reviewer"}}})
		default:
			fmt.Fprint(w, `{"check_runs":[]}`)
		}
	})
	first, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || len(first.Events) != 1 {
		t.Fatal(first, err)
	}
	repeated, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || repeated.Events[0].Key != first.Events[0].Key {
		t.Fatal("identical long source changed identity", err)
	}
	revision.Store(1)
	changed, err := client.readLegacyV1(context.Background(), testConfig())
	if err != nil || len(changed.Events) != 1 {
		t.Fatal(changed, err)
	}
	if first.Events[0].Body != changed.Events[0].Body || first.Events[0].UpdatedAt != changed.Events[0].UpdatedAt || first.Events[0].Key == changed.Events[0].Key {
		t.Fatal("truncated review edit lost its source identity")
	}
}

func TestConfigRejectsEndpointAndCredentialInjection(t *testing.T) {
	for _, repository := range []string{"owner/repo?token=secret", "https://github.com/owner/repo", "owner/../other", "owner/repo\n", "owner/.."} {
		cfg := testConfig()
		cfg.Repository = repository
		if cfg.Validate() == nil {
			t.Fatal("accepted unbounded source", repository)
		}
	}
	cfg := testConfig()
	cfg.CredentialRef = "literal-token"
	if cfg.Validate() == nil {
		t.Fatal("accepted literal credential")
	}
}

type cancellationStore struct{ finished atomic.Int64 }

func (*cancellationStore) BeginGitHubPoll(context.Context, Config) (Ticket, error) {
	return Ticket{Version: 1}, nil
}
func (s *cancellationStore) FinishGitHubPoll(context.Context, Config, Ticket, Batch, string) error {
	s.finished.Add(1)
	return nil
}

func TestWatcherCancellationJoinsInFlightReadWithoutCommit(t *testing.T) {
	entered := make(chan struct{})
	client := peerClient(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() })
	store := &cancellationStore{}
	service := Service{Config: testConfig(), Store: store, Client: client}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("watcher did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not join cancellation")
	}
	if store.finished.Load() != 0 {
		t.Fatal("canceled scan committed")
	}
}
