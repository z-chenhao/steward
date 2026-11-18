package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestCodingProjectFirstEnvironmentMustMatchConcreteApproval(t *testing.T) {
	actual := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET")
	if actual == "" || os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE") == "" {
		t.Skip("requires explicit prepared local container")
	}
	dir, err := os.MkdirTemp("/tmp", "stw-pcbasis-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", actual)
	}}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.Out.URL.Scheme = "http"; r.Out.URL.Host = "steward.local" }, Transport: transport}
	var drift atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" && drift.Load() {
			recorded := httptest.NewRecorder()
			proxy.ServeHTTP(recorded, r)
			var version map[string]any
			if recorded.Code != 200 || json.Unmarshal(recorded.Body.Bytes(), &version) != nil {
				http.Error(w, "fixture version unavailable", 500)
				return
			}
			version["KernelVersion"] = "changed-after-concrete-approval"
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(version)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET", socket)
	s, r, a, _ := queuedCodingProject(t)
	// The first actual basis has not been persisted yet. A later runtime must
	// still match the exact basis returned by status and approved by the owner.
	drift.Store(true)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='failed' AND json_extract(result_json,'$.error.code')='NON_DETERMINISTIC_REPLAY'`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 0 || scalar(t, s, `SELECT count(*) FROM coding_task_environment`) != 0 || scalar(t, s, `SELECT count(*) FROM coding_project_resource`) != 0 {
		t.Fatal("first approval silently rebound a changed actual environment")
	}
}

func TestCodingProjectRecoveryDoesNotLoseDelayedRealCreate(t *testing.T) {
	actual := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET")
	image := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE")
	if actual == "" || image == "" {
		t.Skip("requires explicit prepared local container")
	}
	dir, err := os.MkdirTemp("/tmp", "stw-pclate-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, forwarded := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", actual)
	}}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.Out.URL.Scheme = "http"; r.Out.URL.Host = "steward.local" }, Transport: transport, FlushInterval: -1}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/create") {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 32768))
			if err != nil {
				http.Error(w, "fixture read failed", 500)
				return
			}
			close(entered)
			<-release
			// The request has reached the daemon boundary. A disconnected
			// caller cannot prove that a queued create will never complete.
			forward, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			r = r.Clone(forward)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			proxy.ServeHTTP(w, r)
			close(forwarded)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	defer transport.CloseIdleConnections()
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET", socket)
	s, r, _, _ := queuedCodingProject(t)
	approveCodingCheck(t, s, r)
	var run, invocation, ref string
	if err := s.db.QueryRow(`SELECT i.run_id,i.invocation_id,t.task_ref FROM tool_invocation i JOIN run r USING(run_id) JOIN task t USING(task_id) WHERE i.tool_name='builtin__coding_verify'`).Scan(&run, &invocation, &ref); err != nil {
		t.Fatal(err)
	}
	defer func() {
		unblock.Do(func() { close(release) })
		select {
		case <-forwarded:
		case <-time.After(15 * time.Second):
		}
		client, err := projectcontainer.New(projectcontainer.Config{Socket: actual, Image: image})
		if err == nil {
			defer client.Close()
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.Remove(cleanup, invocation); err != nil {
				t.Error("original delayed fixture resource cleanup", err)
			}
		}
	}()
	ctx := context.Background()
	claim, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	store := r.ToolStore(claim)
	if err := store.Start(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	live, cancel := context.WithCancel(ctx)
	defer cancel()
	type completed struct {
		result tool.Result
		err    error
	}
	done := make(chan completed, 1)
	go func() {
		result, err := store.(tool.HostStore).ExecuteBuiltin(live, tool.Invocation{ID: invocation})
		done <- completed{result, err}
	}()
	received := false
	defer func() {
		cancel()
		if !received {
			<-done
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual create request was not reached")
	}
	cancel()
	out := <-done
	received = true
	if out.err != nil || out.result.Error == nil || out.result.Error.Code != "UNKNOWN_SIDE_EFFECT" {
		t.Fatal("uncertain create was not retained", out)
	}
	if err := store.Finish(ctx, invocation, out.result); err != nil {
		t.Fatal(err)
	}
	if err := r.Release(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	report, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].Status != "unknown" || report.Checks[0].ResourceCleanup != "creation_unknown" || scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE create_started=1 AND creation_observed=0 AND released=0`) != 1 {
		t.Fatal("absence prematurely settled an uncertain create", report, err)
	}
	var scratch string
	if err := s.db.QueryRow(`SELECT directory FROM coding_project_resource`).Scan(&scratch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("pending create lost its recoverable mount", err)
	}
	unblock.Do(func() { close(release) })
	select {
	case <-forwarded:
	case <-time.After(10 * time.Second):
		t.Fatal("delayed actual Engine creation did not complete")
	}
	time.Sleep(5100 * time.Millisecond)
	if err := r.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	report, err = s.CodeReport(ctx, ref, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].Status != "unknown" || report.Checks[0].Project == nil || report.Checks[0].Image != image || report.Checks[0].ResourceCleanup != "confirmed" {
		t.Fatal("post-cleanup original inspection lost uncertainty or input fences", report, err)
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE creation_observed=1 AND released=1`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND attempt_count=1`, invocation) != 1 {
		t.Fatal("delayed create escaped recovery or repeated verification")
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatal("confirmed delayed resource cleanup retained its mount", err)
	}
}

func TestCodingProjectPlanAndReceiptFailureNeverReplayContainerCommands(t *testing.T) {
	for _, boundary := range []string{"plan", "receipt"} {
		t.Run(boundary, func(t *testing.T) {
			s, r, a, _ := queuedCodingProject(t)
			trigger := `CREATE TRIGGER reject_project BEFORE INSERT ON coding_project_plan BEGIN SELECT RAISE(ABORT,'injected plan failure'); END`
			if boundary == "receipt" {
				trigger = `CREATE TRIGGER reject_project BEFORE UPDATE ON tool_invocation WHEN OLD.tool_name='builtin__coding_verify' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`
			}
			if _, err := s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			approveCodingCheck(t, s, r)
			if err := a.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
				t.Fatal("boundary not exercised", err)
			}
			expected := 0
			if boundary == "receipt" {
				expected = 1
			}
			if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != expected {
				t.Fatal("unexpected plan acceptance")
			}
			if expected == 1 && scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 {
				t.Fatal("effect receipt failure lost confirmed resource release")
			}
			if _, err := s.db.Exec(`DROP TRIGGER reject_project`); err != nil {
				t.Fatal(err)
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='unknown' AND attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != expected {
				t.Fatal("uncertain verification replayed or became pass")
			}
		})
	}
}

// Concrete replay of the bounded model's stale-receipt trace: an original
// worker finishes its real container, ownership moves, then its callback arrives.
func TestCodingProjectStaleOwnerCannotPublishReceipt(t *testing.T) {
	s, r, a, _ := queuedCodingProject(t)
	ctx := context.Background()
	approveCodingCheck(t, s, r)
	var run, invocation, ref string
	if err := s.db.QueryRow(`SELECT i.run_id,i.invocation_id,t.task_ref FROM tool_invocation i JOIN run r USING(run_id) JOIN task t USING(task_id) WHERE i.tool_name='builtin__coding_verify'`).Scan(&run, &invocation, &ref); err != nil {
		t.Fatal(err)
	}
	old, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	store := r.ToolStore(old)
	if err := store.Start(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: invocation})
	if err != nil || result.Error != nil || scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 {
		t.Fatal("real original execution did not reach its receipt", result, err)
	}
	if err := r.Release(ctx, old); err != nil {
		t.Fatal(err)
	}
	current, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, invocation, result); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("model counterexample: stale owner published a container receipt", err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND result_json IS NULL`, invocation) != 1 {
		t.Fatal("stale callback changed the original receipt")
	}
	if err := r.Release(ctx, current); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	report, err := s.CodeReport(ctx, ref, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].Status != "unknown" || report.Checks[0].Project == nil || report.Checks[0].Image == "" {
		t.Fatal("real recovery lost original uncertain input/method", report, err)
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND attempt_count=1`, invocation) != 1 {
		t.Fatal("late receipt recovery repeated the effect")
	}
}

func TestCodingProjectLateForgetAndGuardianCannotDispatchPreparedPlan(t *testing.T) {
	for _, point := range []string{"before_coding_project_plan", "after_coding_project_plan"} {
		for _, cause := range []string{"forget", "stop"} {
			t.Run(point+"/"+cause, func(t *testing.T) {
				s, r, _, _ := queuedCodingProject(t)
				approveCodingCheck(t, s, r)
				ctx := context.Background()
				var run, invocation string
				if err := s.db.QueryRow(`SELECT run_id,invocation_id FROM tool_invocation WHERE tool_name='builtin__coding_verify'`).Scan(&run, &invocation); err != nil {
					t.Fatal(err)
				}
				claim, err := r.Claim(ctx, run)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Release(ctx, claim)
				store := r.ToolStore(claim)
				if err := store.Start(ctx, invocation); err != nil {
					t.Fatal(err)
				}
				owner := idAgent(t, s)
				hit := false
				s.MainBoundary = func(at string) {
					if at != point {
						return
					}
					hit = true
					var err error
					if cause == "forget" {
						err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
					} else {
						err = s.Plugins().Stop(ctx, true)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: invocation})
				if !hit || err == nil || result.Text != "" {
					t.Fatal("revoked plan released facts or dispatched", hit, result, err)
				}
				if cause == "forget" && !errors.Is(err, agent.ErrClaimLost) {
					t.Fatal("privacy cause lost", err)
				}
				want := 0
				if point == "after_coding_project_plan" {
					want = 1
				}
				if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != want {
					t.Fatal("unexpected plan acceptance")
				}
				if err := r.recoverProjectResources(ctx); err != nil {
					t.Fatal(err)
				}
				if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=0`) != 0 {
					t.Fatal("revoked pre-dispatch resource not cleaned")
				}
			})
		}
	}
}

// A local protocol proxy rejects only removal, while every execution talks to
// the real prepared Engine. Recovery uses the originally persisted endpoint.
func TestCodingProjectRecoveryCleansOriginalResourceWithoutReverification(t *testing.T) {
	actual := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET")
	if actual == "" {
		t.Skip("requires explicit local container")
	}
	dir, err := os.MkdirTemp("/tmp", "stw-pc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var reject atomic.Bool
	reject.Store(true)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", actual)
	}}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.Out.URL.Scheme = "http"; r.Out.URL.Host = "steward.local" }, Transport: transport, FlushInterval: -1}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() && r.Method == "DELETE" {
			w.WriteHeader(503)
			return
		}
		proxy.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	defer server.Close()
	defer transport.CloseIdleConnections()
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET", socket)
	s, r, a, _ := queuedCodingProject(t)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	var scratch, ref, original string
	if err := s.db.QueryRow(`SELECT x.directory,t.task_ref,p.plan_json FROM coding_project_resource x JOIN coding_project_plan p USING(invocation_id) JOIN tool_invocation i USING(invocation_id) JOIN run r USING(run_id) JOIN task t USING(task_id)`).Scan(&scratch, &ref, &original); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=0`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='unknown'`) != 1 {
		t.Fatal("unconfirmed resource became successful")
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("unknown resource lost its recoverable private files", err)
	}
	reject.Store(false)
	// Respect the persisted backoff, then perform the actual production scan.
	time.Sleep(5100 * time.Millisecond)
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	report, err := s.CodeReport(context.Background(), ref, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].Status != "unknown" || report.Checks[0].Project == nil || report.Checks[0].ResourceCleanup != "confirmed" {
		t.Fatal("post-recovery interaction lost original attempt", report, err)
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND attempt_count=1`) != 1 {
		t.Fatal("recovery repeated verification or failed original cleanup")
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatal("confirmed cleanup retained writable resource", err)
	}
	var retained string
	if err := s.db.QueryRow(`SELECT plan_json FROM coding_project_plan`).Scan(&retained); err != nil || retained != original {
		t.Fatal("recovery rebound original plan", err)
	}
	if report.Checks[0].CandidateSHA256 == "" || report.Checks[0].Command == "" || report.Checks[0].Image == "" {
		t.Fatal("recovery dropped original input/version fences", report.Checks[0])
	}
}
