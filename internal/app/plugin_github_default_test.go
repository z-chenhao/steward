package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
)

type githubDefaultTransport struct {
	source    *url.URL
	transport http.RoundTripper
}

func (p githubDefaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.github.com" {
		return nil, errors.New("foreign source origin")
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = p.source.Scheme
	clone.URL.Host = p.source.Host
	return p.transport.RoundTrip(clone)
}

func TestGitHubDefaultProductionControlPollAndColdLifecycle(t *testing.T) {
	carrier := os.Getenv("STEWARD_TEST_APP_DEFAULT_BUNDLE")
	if carrier == "" {
		t.Skip("requires actual complete40 distribution;required github-plugins system gate supplies it")
	}
	carrier, err := filepath.Abs(carrier)
	if err != nil {
		t.Fatal(err)
	}
	directories, err := builtin.ShippedDirectories(carrier)
	if err != nil || directories.GitHub == "" {
		t.Fatal("actual complete40 distribution missing", err)
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	watch := github.Config{Repository: "owner/repo", PullNumber: 7, CredentialRef: "credential:github-default", PollSeconds: 5}
	cfg.GitHub = &watch
	secrets := secretmemory.New()
	if err = secrets.Put(ctx, watch.CredentialRef, []byte("synthetic-source-only-token")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Header.Get("Authorization") != "Bearer synthetic-source-only-token" {
			t.Error("source credential identity changed")
		}
		switch req.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":1,"login":"owner"}`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(w, `{"number":7,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`)
		case "/repos/owner/repo/pulls/7/comments":
			fmt.Fprint(w, `[{"id":1,"body":"original source","updated_at":"2026-11-01T10:00:00Z","html_url":"https://github.com/owner/repo/pull/7#discussion_r1","user":{"id":2,"login":"reviewer"}}]`)
		case "/repos/owner/repo/pulls/7/reviews":
			fmt.Fprint(w, `[]`)
		default:
			fmt.Fprint(w, `{"check_runs":[]}`)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	openControl := func() *App {
		t.Helper()
		a, err := open(ctx, cfg, "", secrets, true)
		if err != nil {
			t.Fatal(err)
		}
		host := a.Plugins()
		host.Catalog.Launcher = carrier
		a.runtimeRuns().ConfigurePlugins(host.Catalog)
		// Only the trusted executable-path seam is injected. App/Store/policies,
		// full independent bootstrap and fixed-origin verified TLS reads are real.
		a.prepareGitHubPlugins = func(ctx context.Context) error {
			web, err := prepareWebDefault(ctx, host, cfg, directories.Extract)
			if err != nil {
				return err
			}
			return builtin.EnsureDefault(ctx, host, directories, web)
		}
		a.github.Client.HTTP = &http.Client{Transport: githubDefaultTransport{endpoint, server.Client().Transport}}
		return a
	}
	a := openControl()
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	if _, err = a.GitHubStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.GitHubTaskBinding(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := a.Plugins().Store.Status(ctx)
	if err != nil || status.Epoch != 0 || calls.Load() != 0 {
		t.Fatal("read-only control initialized runtime or source", status, err)
	}
	if err = a.PollGitHub(ctx); err != nil {
		t.Fatal(err)
	}
	selected, err := a.Plugins().Store.Status(ctx)
	if err != nil || selected.Epoch != 1 {
		t.Fatal(selected, err)
	}
	graph, err := a.Plugins().Store.Graph(ctx, selected.Revision)
	if err != nil || len(graph.Graph.Bindings) != 40 {
		t.Fatal("source did not use all40", err)
	}
	var source, method string
	if err = a.store.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT content_json,source_method_json FROM github_observation`).Scan(&source, &method)
	}); err != nil {
		t.Fatal(err)
	}
	var frozen github.Method
	if json.Unmarshal([]byte(method), &frozen) != nil || frozen.GraphRevision != selected.Revision {
		t.Fatal("actual App bypassed source method")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openControl()
	if err = a.PollGitHub(ctx); err != nil {
		t.Fatal("first dependent cold poll", err)
	}
	again, err := a.Plugins().Store.Status(ctx)
	if err != nil || again != selected {
		t.Fatal("cold source changed selection", err)
	}
	if _, err = a.Plugins().Store.Deactivate(ctx, "github-default-stop", selected.Revision); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openControl()
	err = a.PollGitHub(ctx)
	var failure *github.Failure
	if !errors.As(err, &failure) || failure.Code != "METHOD_UNAVAILABLE" || calls.Load() != before {
		t.Fatal("cold disabled source restored native", err)
	}
	if err = a.SetGitHubWatch(ctx, false); err != nil {
		t.Fatal("independent owner stop unavailable", err)
	}
}
