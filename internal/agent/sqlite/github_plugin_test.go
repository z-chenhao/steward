package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/agent"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func githubVersionPackage(t *testing.T, version string, fault ...string) string {
	t.Helper()
	directory := t.TempDir()
	path, err := filepath.Abs("../../plugin/githubfollowup/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := string(raw)
	for old, next := range map[string]string{`Version: "1.0.1"`: `Version: "` + version + `"`} {
		if !strings.Contains(code, old) {
			t.Fatal("missing version source")
		}
		code = strings.Replace(code, old, next, 1)
	}
	if version == "1.1.0" {
		for old, next := range map[string]string{`DisplayBytes: 16384`: `DisplayBytes: 1024`, `ObservationBytes: 4096, GoalBytes: 4096`: `ObservationBytes: 512, GoalBytes: 512`, `plan.IntervalSeconds = 60`: `plan.IntervalSeconds = 30`, `[]string{"review_comment", "review", "check_run"}`: `[]string{"check_run", "review", "review_comment"}`} {
			if !strings.Contains(code, old) {
				t.Fatal("missing method variation", old)
			}
			code = strings.Replace(code, old, next, 1)
		}
	}
	if len(fault) > 1 {
		t.Fatal("one explicit adversarial variant")
	}
	if len(fault) == 1 {
		mutations := map[string]string{
			"duplicate":               `plan.Rows=[]int{0,0}`,
			"foreign-row":             `plan.Rows=[]int{count}`,
			"early-complete":          `plan.Next=nil`,
			"replay-page":             `last:=req.History[len(req.History)-1];plan.Next=&contract.Page{Kind:last.Kind,Number:last.Number}`,
			"override-owner-interval": `plan.IntervalSeconds=6`,
			"first-early-complete":    `plan.Next=nil`,
			"first-skip-page":         `plan.Next=&contract.Page{Kind:"review_comment",Number:2}`,
			"first-fabricated-count":  `plan.Next=&contract.Page{Kind:"review_comment",Number:1,Count:1}`,
			"large-method":            `plan.Followup.Method=strings.Repeat("\x00",2048)`,
		}
		mutation, ok := mutations[fault[0]]
		if !ok {
			t.Fatal("unknown adversarial variant")
		}
		needle := "return rsiplugin.JSONResult(ctx, plan)"
		if strings.Count(code, needle) != 1 {
			t.Fatal("missing adversarial return boundary")
		}
		condition := `len(req.History)>0 && count>0`
		if strings.HasPrefix(fault[0], "first-") {
			condition = `len(req.History)==0`
		}
		if fault[0] == "large-method" {
			code = strings.Replace(code, `"errors"`, `"errors";"strings"`, 1)
		}
		code = strings.Replace(code, needle, `if `+condition+` {`+mutation+`};`+needle, 1)
	}
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../../cmd/plugins/github-pr-followup").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	raw, err = os.ReadFile("../../../plugins/github-pr-followup/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func activateGitHubVersion(t *testing.T, h rsi.Host, pkg, version, operation, target string) string {
	t.Helper()
	ctx := context.Background()
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if target == "" {
		p, err := h.Install(ctx, pkg)
		if err != nil {
			t.Fatal(err)
		}
		graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "github-pr-followup", PackageDigest: p.Digest, Config: json.RawMessage(`{}`), StateNamespace: "github-pr-followup", StateVersion: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		target = graph.Revision
	}
	policy := githubOwnerPolicy(version)
	policy.ID = operation
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = h.Propose(ctx, rsi.Candidate{ID: operation, Baseline: status.Revision, Target: target, PolicyID: policy.ID, Goal: "independent exact-PR method iteration"}); err != nil {
		t.Fatal(err)
	}
	decision, err := h.Verify(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Activate(ctx, rsi.Activation{OperationID: operation, Expected: status.Revision, Target: target, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return target
}

func githubLongSource(t *testing.T, id *atomic.Int64, beforeIdentity func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/user":
			if beforeIdentity != nil {
				beforeIdentity(w, req)
			}
			fmt.Fprint(w, `{"id":1,"login":"owner"}`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(w, `{"number":7,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`)
		case "/repos/owner/repo/pulls/7/comments":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": id.Load(), "body": strings.Repeat("你", 6000), "updated_at": "2026-11-01T10:00:00Z", "html_url": "https://github.com/owner/repo/pull/7#discussion_r1", "user": map[string]any{"id": 2, "login": "reviewer"}}})
		case "/repos/owner/repo/pulls/7/reviews":
			fmt.Fprint(w, `[]`)
		case "/repos/owner/repo/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/check-runs":
			fmt.Fprint(w, `{"check_runs":[]}`)
		default:
			t.Errorf("unexpected scoped GET %s", req.URL.RequestURI())
			http.NotFound(w, req)
		}
	}
}

func TestGitHubPluginActualNativeInflightUpgradeRollbackDisableAndCold(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	r := s.Runs(nil, nil)
	first, second := githubVersionPackage(t, "1.0.1"), githubVersionPackage(t, "1.1.0")
	type barrier struct{ entered, resume chan struct{} }
	var mu sync.Mutex
	var held *barrier
	var calls atomic.Int64
	var id atomic.Int64
	id.Store(1)
	service := githubServicePeer(t, s, githubConfig(), githubLongSource(t, &id, func(_ http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		mu.Lock()
		b := held
		held = nil
		mu.Unlock()
		if b != nil {
			close(b.entered)
			select {
			case <-b.resume:
			case <-req.Context().Done():
			}
		}
	}))
	holdAndChange := func(change func()) {
		t.Helper()
		b := &barrier{make(chan struct{}), make(chan struct{})}
		mu.Lock()
		held = b
		mu.Unlock()
		done := make(chan error, 1)
		go func() { done <- service.Poll(ctx) }()
		select {
		case <-b.entered:
		case <-time.After(10 * time.Second):
			close(b.resume)
			t.Fatal("original HTTP scan did not reach identity")
		}
		func() { defer close(b.resume); change() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("retained scan did not settle")
		}
	}
	var host rsi.Host
	holdAndChange(func() { host = installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), first) })
	if scalar(t, s, `SELECT count(*) FROM github_observation WHERE source_method_json IS NULL`) != 1 {
		t.Fatal("activation rewrote an actual in-flight native poll")
	}
	initial, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id.Store(2)
	var upgraded string
	holdAndChange(func() { upgraded = activateGitHubVersion(t, host, second, "1.1.0", "github-real-upgrade", "") })
	check := func(source int, graph string, repeats int, preview int) {
		t.Helper()
		var raw, method string
		if err := s.db.QueryRow(`SELECT content_json,source_method_json FROM github_observation WHERE source_id=?`, source).Scan(&raw, &method); err != nil {
			t.Fatal(err)
		}
		var event github.Observation
		var frozen github.Method
		if json.Unmarshal([]byte(raw), &event) != nil || json.Unmarshal([]byte(method), &frozen) != nil || event.Body != strings.Repeat("你", repeats)+"\n[truncated; open the source URL for full text]" || frozen.GraphRevision != graph || frozen.Recipe.ObservationBytes != preview {
			t.Fatal("frozen body or accepted recipe drifted")
		}
	}
	check(2, initial.Revision, 5461, 4096)
	// Same source under another display version is still exactly one event.
	if err = service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM github_observation`) != 2 {
		t.Fatal("display iteration invented a new source version")
	}
	check(2, initial.Revision, 5461, 4096)
	id.Store(3)
	if err = service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	check(3, upgraded, 341, 512)
	activateGitHubVersion(t, host, "", "1.0.1", "github-real-rollback", initial.Revision)
	id.Store(4)
	if err = service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	check(4, initial.Revision, 5461, 4096)
	if _, err = host.Store.Deactivate(ctx, "github-real-disable", initial.Revision); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	id.Store(5)
	err = service.Poll(ctx)
	var failure *github.Failure
	if !errors.As(err, &failure) || failure.Code != "METHOD_UNAVAILABLE" || calls.Load() != before || scalar(t, s, `SELECT count(*) FROM github_observation`) != 4 {
		t.Fatal("deactivation restored native IO or created observations", err)
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(s.path)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	r = s.Runs(nil, nil)
	r.ConfigurePlugins(host.Catalog)
	service.Store = s
	service.ReadStep = s.ReadGitHubStep
	err = service.Poll(ctx)
	if !errors.As(err, &failure) || failure.Code != "METHOD_UNAVAILABLE" || calls.Load() != before {
		t.Fatal("first cold dependent action revived native IO", err)
	}
	cold, err := s.Plugins().Status(ctx)
	if err != nil || cold != status {
		t.Fatal("cold recovery repaired or changed selection", err)
	}
}

func githubQueuedBody(t *testing.T, s *Store) (string, string) {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=(SELECT run_id FROM github_task_followup ORDER BY created_at DESC LIMIT 1)`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cp agent.Checkpoint
	if json.Unmarshal([]byte(raw), &cp) != nil {
		t.Fatal("bad queued checkpoint")
	}
	for _, m := range cp.Messages {
		if m.Origin == "runtime" && m.Source != nil && m.Source.Kind == "GitHub observation" {
			for _, part := range m.Content {
				if part.Type == "text" {
					_, data, ok := strings.Cut(part.Text, "\n")
					if !ok {
						t.Fatal("missing runtime payload")
					}
					var payload struct {
						Content struct {
							Observation github.Observation `json:"observation"`
							Boundary    string             `json:"authorization_boundary"`
						} `json:"content"`
					}
					if json.Unmarshal([]byte(data), &payload) != nil || !strings.Contains(payload.Content.Boundary, "at most once") || !m.Source.Protected {
						t.Fatal("protected source authority changed")
					}
					return payload.Content.Observation.Body, raw
				}
			}
		}
	}
	t.Fatal("actual queued Main source input absent")
	return "", ""
}

func TestGitHubPluginAcceptedRecipeMainScopeAndSourceGuardian(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(fmt.Sprint(quarantine), func(t *testing.T) {
			ctx := context.Background()
			s, r, a, _, cfg, ref := continuationFixture(t, "once")
			watchBatch(t, s, cfg, "")
			bindTask(t, s, cfg, ref)
			host := installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), githubVersionPackage(t, "1.0.1"))
			original, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var id atomic.Int64
			id.Store(1)
			service := githubServicePeer(t, s, cfg, githubLongSource(t, &id, nil))
			if err = service.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			activateGitHubVersion(t, host, githubVersionPackage(t, "1.1.0"), "1.1.0", "github-before-source-main", "")
			if err = r.recoverGitHubFollowups(ctx); err != nil {
				t.Fatal(err)
			}
			body, before := githubQueuedBody(t, s)
			if body != strings.Repeat("你", 1365)+"\n[Preview truncated; consult the original Task/source before relying on missing details.]" {
				t.Fatal("old accepted source consumed new 512byte method")
			}
			selected, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if quarantine {
				if err = host.Store.Quarantine(ctx, original.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = host.Store.Deactivate(ctx, "github-after-source-queued", selected.Revision); err != nil {
					t.Fatal(err)
				}
			}
			_, after := githubQueuedBody(t, s)
			if after != before {
				t.Fatal("control change rewrote queued input")
			}
			mustDrain(t, a)
			want := 1
			if quarantine {
				want = 0
			}
			if scalar(t, s, `SELECT count(*) FROM task_instruction`) != want || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
				t.Fatal("source quarantine or original same-Task append authority changed")
			}
			if !quarantine {
				mustDrain(t, a)
				if scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 || scalar(t, s, `SELECT count(*) FROM github_task_followup WHERE state='continued'`) != 1 {
					t.Fatal("accepted recipe appended more than once")
				}
			}
		})
	}
}

func TestGitHubV67SourceMethodMigrationKeepsOriginalPendingMainAndRows(t *testing.T) {
	ctx := context.Background()
	s, r, _, _, cfg, ref := continuationFixture(t, "once")
	watchBatch(t, s, cfg, "")
	bindTask(t, s, cfg, ref)
	watchBatch(t, s, cfg, "accepted-native-before-method")
	if err := r.recoverGitHubFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(removeGitHubSourceMethodFixtureSchema + `PRAGMA user_version=67`); err != nil {
		t.Fatal(err)
	}
	snapshot := func(db *sql.DB) map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, table := range []string{"github_watch", "github_observation", "github_task_binding", "github_task_followup", "run", "rsi_run", "capability_snapshot", "run_context_policy", "model_generation", "tool_invocation", "agent_invocation", "model_snapshot"} {
			query := `SELECT * FROM ` + table + ` ORDER BY 1`
			if table == "model_generation" {
				query = `SELECT ` + historicalGenerationColumnsV80 + ` FROM model_generation ORDER BY 1`
			}
			if table == "github_watch" {
				query = `SELECT scope,repository,pull_number,repository_id,enabled,intent_privacy_epoch,state,failure_code,head_sha,last_attempt_at,last_success_at,version FROM github_watch ORDER BY scope`
			}
			if table == "github_observation" {
				query = `SELECT sequence,scope,event_key,kind,source_id,content_json,received_at,intent_privacy_epoch,binding_generation FROM github_observation ORDER BY sequence`
			}
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			values := [][]any{}
			for rows.Next() {
				items := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range items {
					pointers[i] = &items[i]
				}
				if err = rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				values = append(values, items)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out[table] = string(raw)
		}
		return out
	}
	before := snapshot(s.db)
	root := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	after := snapshot(next.db)
	if scalar(t, next, `SELECT count(*) FROM model_generation WHERE reference_rates_managed!=0`) != 0 || scalar(t, next, `SELECT count(*) FROM generation_reference_rates`) != 0 {
		t.Fatal("migration opted historical generations into new pricing")
	}
	for table, rows := range before {
		if after[table] != rows {
			t.Fatal("forward migration rewrote frozen native evidence", table)
		}
	}
	if version := scalar(t, next, `PRAGMA user_version`); version != len(migrations) {
		t.Fatal("forward migration did not reach the actual source catalogue", version, len(migrations))
	}
	if scalar(t, next, `SELECT count(*) FROM github_observation WHERE source_method_json IS NULL`) != 1 {
		t.Fatal("original observations lost explicit native provenance")
	}
	if _, err = next.db.Exec(`UPDATE github_observation SET source_method_json='{"graph_revision":"forged","recipe":{}}'`); err == nil || !strings.Contains(err.Error(), "immutable GitHub observation") {
		t.Fatal("source method is mutable", err)
	}
}

func TestGitHubPluginLateForgetCannotAcceptSourceOrRecipe(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.Runs(nil, nil)
	owner := idAgent(t, s)
	installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), githubVersionPackage(t, "1.0.1"))
	var id atomic.Int64
	id.Store(1)
	service := githubServicePeer(t, s, githubConfig(), githubLongSource(t, &id, nil))
	var revoked bool
	service.ReadStep = func(ctx context.Context, cfg github.Config, ticket github.Ticket, req contract.Request, facts []byte) (contract.Plan, error) {
		plan, err := s.ReadGitHubStep(ctx, cfg, ticket, req, facts)
		if err != nil {
			return plan, err
		}
		if len(facts) > 0 && !revoked {
			revoked = true
			if err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
				t.Fatal(err)
			}
		}
		return plan, nil
	}
	if err = service.Poll(ctx); err == nil {
		t.Fatal("late Forget accepted a source")
	}
	if !revoked || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 || scalar(t, s, `SELECT count(*) FROM github_task_followup`) != 0 || scalar(t, s, `SELECT count(*) FROM run`) != 0 {
		t.Fatal("forgotten source or method became durable work")
	}
	status, err := s.GitHubStatus(ctx, githubConfig())
	if err != nil || status.State != "privacy_stopped" {
		t.Fatal("Forget was hidden behind method failure", status, err)
	}
}

func TestGitHubPluginPreservesOriginalRowAndQuotaFailureOrder(t *testing.T) {
	pkg := githubVersionPackage(t, "1.0.1")
	for _, mode := range []string{"history-final-invalid-time", "quota-before-later-invalid-identity", "exactly-full", "one-over-limit", "lookahead-failed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), pkg)
			var reached int
			service := githubServicePeer(t, s, githubConfig(), func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"id":1,"login":"owner"}`)
				case "/repos/owner/repo/pulls/7":
					fmt.Fprint(w, `{"number":7,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`)
				case "/repos/owner/repo/pulls/7/comments":
					var page int
					if _, err := fmt.Sscan(req.URL.Query().Get("page"), &page); err != nil {
						t.Error(err)
					}
					reached = page
					entries := []map[string]any{}
					count := 100
					if page == 21 {
						switch mode {
						case "exactly-full":
							count = 0
						case "one-over-limit":
							count = 1
						case "lookahead-failed":
							http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
							return
						default:
							t.Error("unexpected probe after failure")
							http.Error(w, "unexpected", 500)
							return
						}
					}
					for i := 0; i < count; i++ {
						body := "feedback"
						if mode == "quota-before-later-invalid-identity" {
							body = strings.Repeat("x", 16384)
						}
						entry := map[string]any{"id": (page-1)*100 + i + 1, "body": body, "updated_at": "2026-11-01T10:00:00Z", "html_url": "https://github.com/owner/repo/pull/7#discussion_r1", "user": map[string]any{"id": 2, "login": "reviewer"}}
						if mode == "history-final-invalid-time" && page == 20 && i == 99 {
							delete(entry, "updated_at")
						}
						if mode == "quota-before-later-invalid-identity" && page == 5 && i == 99 {
							entry["id"] = 0
						}
						entries = append(entries, entry)
					}
					_ = json.NewEncoder(w).Encode(entries)
				case "/repos/owner/repo/pulls/7/reviews":
					fmt.Fprint(w, "[]")
				case "/repos/owner/repo/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/check-runs":
					fmt.Fprint(w, `{"check_runs":[]}`)
				default:
					t.Error("partial failed scan advanced to another source")
					http.Error(w, "unexpected", 500)
				}
			})
			code, last := "INVALID_RESPONSE", 20
			if mode == "quota-before-later-invalid-identity" {
				code, last = "HISTORY_LIMIT", 5
			}
			err = service.Poll(ctx)
			if mode == "exactly-full" {
				if err != nil || reached != 21 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 2000 {
					t.Fatal("full final page must be admitted", err, reached)
				}
				return
			}
			if mode == "one-over-limit" {
				code, last = "HISTORY_LIMIT", 21
			}
			if mode == "lookahead-failed" {
				if err == nil || reached != 21 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 {
					t.Fatal("unconfirmed final page advanced progress", err, reached)
				}
				return
			}
			var failure *github.Failure
			if !errors.As(err, &failure) || failure.Code != code || reached != last || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 {
				t.Fatal("original sequential failure precedence or whole-scan boundary changed", err, reached)
			}
		})
	}
}

func TestGitHubPluginDefaultCadenceAndReadOnlyHealthUseAcceptedInterval(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.Runs(nil, nil)
	installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.1.0"), githubVersionPackage(t, "1.1.0"))
	var id atomic.Int64
	id.Store(1)
	var paths []string
	base := githubLongSource(t, &id, nil)
	cfg := githubConfig()
	cfg.PollSeconds = 0
	service := githubServicePeer(t, s, cfg, func(w http.ResponseWriter, req *http.Request) { paths = append(paths, req.URL.Path); base(w, req) })
	if err = service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"/user", "/repos/owner/repo/pulls/7", "/repos/owner/repo/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/check-runs", "/repos/owner/repo/pulls/7/reviews", "/repos/owner/repo/pulls/7/comments"}
	a, _ := json.Marshal(paths)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		t.Fatal("workflow order was still owned by native Host", paths)
	}
	if scalar(t, s, `SELECT selected_interval_seconds FROM github_watch`) != 30 {
		t.Fatal("selected default cadence was not accepted")
	}
	if _, err = s.db.Exec(`UPDATE github_watch SET last_success_at=?,version=version+1`, time.Now().Add(-61*time.Second).UnixMicro()); err != nil {
		t.Fatal(err)
	}
	before := len(paths)
	status, err := s.GitHubStatus(ctx, cfg)
	if err != nil || status.State != "stale" || len(paths) != before {
		t.Fatal("status used native60s default or dispatched policy/source IO", status, err)
	}
	// Explicit owner interval remains exact even with this different default.
	service.Config.PollSeconds = 5
	if err = service.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT selected_interval_seconds FROM github_watch`) != 5 {
		t.Fatal("method overrode explicit owner interval")
	}
}

func TestGitHubPluginMechanicallyAcceptedBadPlansCannotCommitPartialOrEscape(t *testing.T) {
	for _, fault := range []string{"duplicate", "foreign-row", "early-complete", "replay-page", "override-owner-interval"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			// This explicit adversarial owner gate covers only the first step. The
			// candidate really passes it; Host authority must still reject the later
			// bad plan rather than trust a successful initial verification.
			installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.2.0"), githubVersionPackage(t, "1.2.0", fault))
			var id atomic.Int64
			id.Store(1)
			var reads int
			base := githubLongSource(t, &id, nil)
			service := githubServicePeer(t, s, githubConfig(), func(w http.ResponseWriter, req *http.Request) { reads++; base(w, req) })
			err = service.Poll(ctx)
			var failure *github.Failure
			if !errors.As(err, &failure) || failure.Code != "INVALID_METHOD" || reads != 3 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 || scalar(t, s, `SELECT count(*) FROM run`) != 0 {
				t.Fatal("bad method escaped original row/page/owner/completeness guard", fault, err, reads)
			}
			status, err := s.GitHubStatus(ctx, githubConfig())
			if err != nil || status.LastSuccess != nil || status.State != "interrupted" {
				t.Fatal("partial scan claimed a successful boundary", status, err)
			}
		})
	}
}

type githubObservedSecrets struct {
	llm.SecretStore
	reads atomic.Int64
}

func (s *githubObservedSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	s.reads.Add(1)
	return s.SecretStore.Get(ctx, ref)
}

func TestGitHubPluginBadInitialPlanCannotReadCredentialOrSource(t *testing.T) {
	for _, fault := range []string{"first-early-complete", "first-skip-page", "first-fabricated-count"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			policy := githubOwnerPolicy("1.2.0")
			var expected map[string]any
			if err = json.Unmarshal(policy.Cases[0].Expected, &expected); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "first-early-complete":
				expected["next"] = nil
			case "first-skip-page":
				expected["next"] = map[string]any{"kind": "review_comment", "number": 2, "count": 0}
			case "first-fabricated-count":
				expected["next"] = map[string]any{"kind": "review_comment", "number": 1, "count": 1}
			}
			policy.Cases[0].Expected, _ = json.Marshal(expected)
			installRuntimePackage(t, s, r, "github-pr-followup", policy, githubVersionPackage(t, "1.2.0", fault))
			var id atomic.Int64
			id.Store(1)
			var reads atomic.Int64
			base := githubLongSource(t, &id, nil)
			service := githubServicePeer(t, s, githubConfig(), func(w http.ResponseWriter, req *http.Request) { reads.Add(1); base(w, req) })
			secrets := &githubObservedSecrets{SecretStore: service.Client.Secrets}
			service.Client.Secrets = secrets
			err = service.Poll(ctx)
			var failure *github.Failure
			if !errors.As(err, &failure) || failure.Code != "INVALID_METHOD" || reads.Load() != 0 || secrets.reads.Load() != 0 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 {
				t.Fatal("bad initial plan acquired source or credential authority", err, reads.Load(), secrets.reads.Load())
			}
		})
	}
}

func TestGitHubPluginLastStepGuardianAndActualStorageFailureStayDistinct(t *testing.T) {
	for _, mode := range []string{"quarantine", "storage"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			host := installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), githubVersionPackage(t, "1.0.1"))
			var id atomic.Int64
			id.Store(1)
			service := githubServicePeer(t, s, githubConfig(), githubLongSource(t, &id, nil))
			changed := false
			service.ReadStep = func(ctx context.Context, cfg github.Config, ticket github.Ticket, request contract.Request, facts []byte) (contract.Plan, error) {
				plan, err := s.ReadGitHubStep(ctx, cfg, ticket, request, facts)
				if err != nil {
					return plan, err
				}
				if len(request.History) == 3 && !changed {
					changed = true
					if mode == "quarantine" {
						err = host.Store.Quarantine(ctx, ticket.GraphRevision)
					} else {
						_, err = s.db.Exec(`DROP TABLE rsi_quarantine`)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				return plan, nil
			}
			err = service.Poll(ctx)
			if !changed || err == nil || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 {
				t.Fatal("last-step authority loss committed observations", err)
			}
			var failure *github.Failure
			if mode == "quarantine" {
				status, e := s.GitHubStatus(ctx, githubConfig())
				if !errors.As(err, &failure) || failure.Code != "METHOD_UNAVAILABLE" || e != nil || status.State != "interrupted" || status.FailureCode != "METHOD_UNAVAILABLE" || status.LastSuccess != nil {
					t.Fatal("Guardian loss was hidden as storage/success", err, status, e)
				}
			} else {
				var operation *agent.OperationError
				if errors.As(err, &failure) || !errors.As(err, &operation) || !errors.Is(err, agent.ErrStorage) {
					t.Fatal("actual storage failure was invented as policy rejection", err)
				}
			}
		})
	}
}

func TestGitHubPluginExpandedMethodSpendsOriginalScanBudget(t *testing.T) {
	for _, mode := range []string{"default", "large-method"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			version := "1.0.1"
			pkg := ""
			if mode == "large-method" {
				version = "1.2.0"
				pkg = githubVersionPackage(t, version, mode)
			} else {
				pkg = githubVersionPackage(t, version)
			}
			installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy(version), pkg)
			var id atomic.Int64
			id.Store(1)
			base := githubLongSource(t, &id, nil)
			lastPage := 0
			service := githubServicePeer(t, s, githubConfig(), func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/repos/owner/repo/pulls/7/comments" {
					base(w, req)
					return
				}
				var page int
				fmt.Sscan(req.URL.Query().Get("page"), &page)
				lastPage = page
				count := 100
				if page == 11 {
					count = 1
				}
				rows := []map[string]any{}
				for i := 0; i < count; i++ {
					rows = append(rows, map[string]any{"id": (page-1)*100 + i + 1, "body": "feedback", "updated_at": "2026-11-01T10:00:00Z", "html_url": "https://github.com/owner/repo/pull/7#discussion_r1", "user": map[string]any{"id": 2, "login": "reviewer"}})
				}
				_ = json.NewEncoder(w).Encode(rows)
			})
			err = service.Poll(ctx)
			if mode == "default" {
				if err != nil || lastPage != 11 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 1001 {
					t.Fatal("default recipe changed original source budget", err, lastPage)
				}
			} else {
				var failure *github.Failure
				if !errors.As(err, &failure) || failure.Code != "HISTORY_LIMIT" || lastPage >= 11 || scalar(t, s, `SELECT count(*) FROM github_observation`) != 0 {
					t.Fatal("escaped method text exceeded unpriced scan budget", err, lastPage)
				}
			}
		})
	}
}

// Independent literal first-step owner expectation; it is not a candidate call.
func githubOwnerPolicy(version string) rsi.Policy {
	display, preview := 16384, 4096
	nextKind := "review_comment"
	if version == "1.1.0" {
		display, preview = 1024, 512
		nextKind = "check_run"
	}
	expected := fmt.Sprintf(`{"next":{"kind":%q,"number":1,"count":0},"rows":[],"display_bytes":%d,"display_suffix":"\n[truncated; open the source URL for full text]","followup":{"observation_bytes":%d,"goal_bytes":%d,"notice":"\n[Preview truncated; consult the original Task/source before relying on missing details.]"},"interval_seconds":5}`, nextKind, display, preview, preview)
	return rsi.Policy{ID: "github-owner-" + version, AllowedContracts: []string{contract.Contract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{{InstanceID: "github-pr-followup", Contract: contract.Contract, Input: json.RawMessage(`{"history":[],"poll_seconds":5}`), Inputs: []rsi.InputData{{ID: contract.InputID, Data: []byte{}}}, Expected: json.RawMessage(expected)}}}
}

func githubServicePeer(t *testing.T, s *Store, cfg github.Config, handler http.HandlerFunc) github.Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretmemory.New()
	if err = secrets.Put(context.Background(), cfg.CredentialRef, []byte("synthetic-test-token")); err != nil {
		t.Fatal(err)
	}
	return github.Service{Config: cfg, Store: s, ReadStep: s.ReadGitHubStep, Client: github.Client{Secrets: secrets, HTTP: &http.Client{Transport: continuationPeer{endpoint, http.DefaultTransport}, Timeout: 20 * time.Second}}}
}

func TestGitHubPluginActualWholeScanMatchesOriginal27Oracles(t *testing.T) {
	ctx := context.Background()
	pkg := githubVersionPackage(t, "1.0.1")
	raw, err := os.ReadFile("../../observer/github/testdata/native-reader-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string `json:"name"`
		HTTP []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"http"`
		Batch github.Batch `json:"batch"`
		Error string       `json:"error"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) != 27 {
		t.Fatal("original source oracles incomplete", err)
	}
	for _, test := range cases {
		// v1.0.0 stopped at the full twentieth page. The current method
		// requires one additional source response to prove overflow.
		if test.Name == "history-limit" {
			probe := test.HTTP[len(test.HTTP)-1]
			probe.Path = "/repos/owner/repo/pulls/7/comments?per_page=100&page=21"
			test.HTTP = append(test.HTTP, probe)
		}
		t.Run(test.Name, func(t *testing.T) {
			s, err := Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := s.Runs(nil, nil)
			installRuntimePackage(t, s, r, "github-pr-followup", githubOwnerPolicy("1.0.1"), pkg)
			cursor := 0
			service := githubServicePeer(t, s, githubConfig(), func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-test-token" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
					t.Error("source authority changed")
				}
				if cursor >= len(test.HTTP) {
					t.Errorf("extra HTTP %s", r.URL.RequestURI())
					http.Error(w, "unexpected", 404)
					return
				}
				e := test.HTTP[cursor]
				cursor++
				if r.URL.RequestURI() != e.Path {
					t.Errorf("original source progression changed: got %s want %s", r.URL.RequestURI(), e.Path)
				}
				body := e.Body
				if body == "<4194305 spaces>" {
					body = strings.Repeat(" ", (4<<20)+1)
				}
				if body == "<hex:5bff5d>" {
					body = "[\xff]"
				}
				w.Header().Set("Location", "https://attacker.invalid/collect")
				w.Header().Set("Link", `<https://attacker.invalid/collect>; rel="next"`)
				w.WriteHeader(e.Status)
				fmt.Fprint(w, body)
			})
			err = service.Poll(ctx)
			code := ""
			if err != nil {
				var failure *github.Failure
				if !errors.As(err, &failure) {
					t.Fatal(err)
				}
				code = failure.Code
			}
			if code != test.Error || cursor != len(test.HTTP) {
				t.Fatalf("failure/full HTTP trace: %q want%q reads%d/%d", code, test.Error, cursor, len(test.HTTP))
			}
			var actual github.Batch
			if code == "" {
				status, err := s.GitHubStatus(ctx, githubConfig())
				if err != nil {
					t.Fatal(err)
				}
				actual.RepositoryID = *status.RepositoryID
				actual.HeadSHA = status.HeadSHA
			}
			rows, err := s.db.Query(`SELECT content_json,source_method_json FROM github_observation ORDER BY sequence`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var text, method string
				if err = rows.Scan(&text, &method); err != nil {
					t.Fatal(err)
				}
				var event github.Observation
				var frozen github.Method
				if json.Unmarshal([]byte(text), &event) != nil || json.Unmarshal([]byte(method), &frozen) != nil || frozen.GraphRevision == "" || frozen.Recipe.ObservationBytes != 4096 {
					t.Fatal("missing immutable original method")
				}
				actual.Events = append(actual.Events, event)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(actual)
			want, _ := json.Marshal(test.Batch)
			if !bytes.Equal(got, want) {
				t.Fatal("managed event body/key/identity/order or partial scan changed")
			}
			if scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal("source observation created a Run or second effect ledger")
			}
		})
	}
}
