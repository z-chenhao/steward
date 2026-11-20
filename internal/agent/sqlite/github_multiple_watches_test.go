package sqlite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/task"
)

func TestGitHubTwoExplicitPRsRetainSeparateTasksAndRevocation(t *testing.T) {
	ctx := context.Background()
	s, r, a, model, first, firstTask := continuationFixture(t, "once")
	mustSubmit(t, a, "second task", "Maintain the other PR as a separate task.")
	mustDrain(t, a)
	var secondTask string
	if err := s.db.QueryRow(`SELECT task_ref FROM task WHERE task_ref!=?`, firstTask).Scan(&secondTask); err != nil {
		t.Fatal(err)
	}
	second := first
	second.PullNumber++
	watches := []github.Config{first, second}
	if err := s.ConfigureGitHubWatches(ctx, watches); err != nil {
		t.Fatal(err)
	}
	installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.0"), githubVersionPackage(t, "1.0.0"))
	var activeRef string
	oldRespond := model.respond
	model.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isGitHubFollowup(req) && !githubToolReturned(req) {
			return calls(task.ManageName, `{"operation":"append","taskRef":"`+activeRef+`","instruction":"Address this PR observation."}`), nil
		}
		return oldRespond(req)
	}
	var revisions [2]atomic.Int64
	var services []github.Service
	for index, watch := range watches {
		base := fmt.Sprintf("/repos/%s/pulls/%d", watch.Repository, watch.PullNumber)
		peer := githubServicePeer(t, s, watch, func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/user":
				fmt.Fprint(w, `{"id":1,"login":"owner"}`)
			case base:
				fmt.Fprintf(w, `{"number":%d,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`, watch.PullNumber)
			case base + "/comments":
				if revisions[index].Load() == 0 {
					fmt.Fprint(w, "[]")
					return
				}
				fmt.Fprintf(w, `[{"id":13,"body":"review %d","updated_at":"2026-11-01T10:00:00Z","html_url":"https://github.com/owner/repo/pull/%d#discussion_r13","user":{"id":2,"login":"reviewer"}}]`, revisions[index].Load(), watch.PullNumber)
			case base + "/reviews":
				fmt.Fprint(w, "[]")
			case "/repos/owner/repo/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/check-runs":
				fmt.Fprint(w, `{"check_runs":[]}`)
			default:
				t.Error("unexpected source", req.URL.Path)
				http.NotFound(w, req)
			}
		})
		if err := peer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		ref := []string{firstTask, secondTask}[index]
		if err := s.BindGitHubTaskIn(ctx, watch, watches, github.BindingRequest{TaskRef: ref, TaskEpoch: 1, Mode: "automatic", EventKind: "review_comment"}, true); err != nil {
			t.Fatal(err)
		}
		services = append(services, peer)
	}
	for index, peer := range services {
		activeRef = []string{firstTask, secondTask}[index]
		revisions[index].Store(1)
		if err := peer.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		mustDrain(t, a)
		if !s.hasContinuedPR(t, peer.Config.Scope(), activeRef, 1) {
			t.Fatal("wrong Task continuation", activeRef, peer.Config.Scope())
		}
	}
	var seq int64
	if err := s.db.QueryRow(`SELECT min(sequence) FROM github_observation WHERE scope=?`, first.Scope()).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	q := evidence.Request{Ref: fmt.Sprintf("github:%d", seq)}
	if _, err := r.ReadGitHubEvidenceForWatches(ctx, q, watches); err != nil {
		t.Fatal("missing evidence for explicitly selected watch", err)
	}
	if _, err := r.ReadGitHubEvidenceForWatches(ctx, q, []github.Config{second}); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("foreign watch leaked through owner evidence", err)
	}
	before, err := s.GitHubTaskBinding(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureGitHubWatches(ctx, []github.Config{second}); err != nil {
		t.Fatal(err)
	}
	after, err := s.GitHubTaskBinding(ctx, second)
	if err != nil || after.Generation != before.Generation || after.Version != before.Version {
		t.Fatal("removing one scope disturbed its sibling", before, after, err)
	}
	inactive, err := s.GitHubTaskBinding(ctx, first)
	if err != nil || inactive.State != "stopped" {
		t.Fatal("old binding retained", inactive, err)
	}
	if _, err = r.ReadGitHubEvidenceForWatches(ctx, q, watches); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("revoked evidence remained visible", err)
	}
	activeRef = secondTask
	revisions[1].Store(2)
	if err = services[1].Poll(ctx); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if !s.hasContinuedPR(t, second.Scope(), secondTask, 2) || !s.hasContinuedPR(t, first.Scope(), firstTask, 1) {
		t.Fatal("surviving scope failed to continue independently")
	}
}
func (s *Store) hasContinuedPR(t *testing.T, scope, taskRef string, count int) bool {
	t.Helper()
	var got int
	if err := s.db.QueryRow(`SELECT count(*) FROM github_task_followup f JOIN task t USING(task_id) WHERE f.scope=? AND t.task_ref=? AND f.state='continued'`, scope, taskRef).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got == count
}
