package sqlite

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/observer/github"
)

func TestGitHubConversationCommentRequiresExactRuleAndContinuesOnlyBoundTask(t *testing.T) {
	ctx := context.Background()
	s, r, actor, _, cfg, ref := continuationFixture(t, "once")
	installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.2"), githubVersionPackage(t, "1.0.2"))
	var reads, revision atomic.Int64
	var invalid, revoke atomic.Bool
	peer := githubServicePeer(t, s, cfg, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/user":
			fmt.Fprint(w, `{"id":1,"login":"owner"}`)
		case req.URL.Path == "/repos/owner/repo/pulls/7":
			fmt.Fprint(w, `{"number":7,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`)
		case req.URL.Path == "/repos/owner/repo/issues/7/comments":
			reads.Add(1)
			if !strings.HasSuffix(req.URL.RawQuery, "page=1") {
				t.Error("unexpected page", req.URL.RawQuery)
			}
			if revoke.Load() {
				b, err := s.GitHubTaskBinding(ctx, cfg)
				if err != nil {
					t.Error(err)
				} else if err = s.BindGitHubTask(ctx, cfg, github.BindingRequest{Version: b.Version}, false); err != nil {
					t.Error(err)
				}
			}
			issue := 7
			if invalid.Load() {
				issue = 8
			}
			fmt.Fprintf(w, `[{"id":12,"body":"review by owner","updated_at":"2026-11-01T10:00:00Z","html_url":"https://github.com/owner/repo/pull/7#issuecomment-12","issue_url":"https://api.github.com/repos/owner/repo/issues/7","user":{"id":1,"login":"owner"}},{"id":13,"body":"follow up %d","updated_at":"2026-11-01T10:01:00Z","html_url":"https://github.com/owner/repo/pull/7#issuecomment-13","issue_url":"https://api.github.com/repos/owner/repo/issues/%d","user":{"id":2,"login":"reviewer"}}]`, revision.Load(), issue)
		case req.URL.Path == "/repos/owner/repo/pulls/7/comments" || req.URL.Path == "/repos/owner/repo/pulls/7/reviews":
			fmt.Fprint(w, "[]")
		case strings.HasSuffix(req.URL.Path, "/check-runs"):
			fmt.Fprint(w, `{"check_runs":[]}`)
		default:
			t.Error("out of scope source", req.URL.Path)
			http.NotFound(w, req)
		}
	})
	poll := func() {
		t.Helper()
		if err := peer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	if reads.Load() != 0 {
		t.Fatal("read conversations without opt-in")
	}
	bind := func(mode string) {
		t.Helper()
		b, err := s.GitHubTaskBinding(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.BindGitHubTask(ctx, cfg, github.BindingRequest{TaskRef: ref, TaskEpoch: 1, Version: b.Version, Mode: mode, EventKind: "issue_comment"}, true); err != nil {
			t.Fatal(err)
		}
	}
	bind("observe")
	poll()
	poll()
	mustDrain(t, actor)
	decisions, err := s.GitHubRuleDecisions(ctx, cfg, 0)
	if err != nil || len(decisions) != 1 || !decisions[0].Matched || decisions[0].TaskRef != ref || decisions[0].Kind != "issue_comment" || scalar(t, s, `SELECT count(*) FROM github_task_followup`) != 0 {
		t.Fatal("observation or exact owner exclusion", decisions, err)
	}
	bind("automatic")
	revision.Add(1)
	invalid.Store(true)
	if err = peer.Poll(ctx); err == nil {
		t.Fatal("foreign issue was accepted")
	}
	if scalar(t, s, `SELECT count(*) FROM github_observation`) != 1 {
		t.Fatal("partial new batch committed")
	}
	invalid.Store(false)
	poll()
	poll()
	mustDrain(t, actor)
	if scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM github_task_followup WHERE state='continued'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 {
		t.Fatal("comment did not continue the original task exactly once")
	}
	revision.Add(1)
	revoke.Store(true)
	if err = peer.Poll(ctx); err == nil {
		t.Fatal("accepted comment after concurrent revocation")
	}
	if scalar(t, s, `SELECT count(*) FROM github_observation`) != 2 {
		t.Fatal("retained revoked source")
	}
	before := reads.Load()
	revoke.Store(false)
	poll()
	if reads.Load() != before {
		t.Fatal("retried private conversation read after rule closed")
	}
}
