package main

import (
	"bytes"
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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/observer/github"
)

type githubPeerTransport struct {
	peer       *url.URL
	underlying http.RoundTripper
}

func (p githubPeerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
		return nil, errors.New("unexpected external endpoint")
	}
	clone := r.Clone(r.Context())
	clone.URL.Scheme = p.peer.Scheme
	clone.URL.Host = p.peer.Host
	return p.underlying.RoundTrip(clone)
}

func TestGitHubCLISuccessfulPollPersistsDedupAndFailureBoundary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEWARD_DATA_ROOT", filepath.Join(root, "data"))
	t.Setenv("GITHUB_OBSERVER_SYNTHETIC_TEST", "synthetic-observer-token")
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("version: 1\ngithub:\n  repository: owner/repo\n  pull_number: 7\n  credential_ref: env:GITHUB_OBSERVER_SYNTHETIC_TEST\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	var revision atomic.Int64
	var checkRevision atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-observer-token" {
			t.Error("wrong authentication boundary")
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":1,"login":"steward"}`)
		case "/repos/owner/repo/pulls/7":
			fmt.Fprint(w, `{"number":7,"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"repo":{"id":99,"full_name":"owner/repo"}}}`)
		case "/repos/owner/repo/pulls/7/comments":
			fmt.Fprintf(w, `[{"id":5,"body":"requested change %d","updated_at":"2026-11-01T00:00:00Z","html_url":"https://github.com/owner/repo/pull/7#discussion_r5","user":{"id":2,"login":"reviewer"}}]`, revision.Load())
		case "/repos/owner/repo/pulls/7/reviews":
			if fail.Load() {
				http.Error(w, "synthetic private provider failure", http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `[]`)
		case "/repos/owner/repo/commits/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/check-runs":
			revision := checkRevision.Load()
			if revision == 0 {
				fmt.Fprint(w, `{"check_runs":[]}`)
				return
			}
			conclusion := "success"
			if revision == 2 {
				conclusion = "failure"
			}
			fmt.Fprintf(w, `{"check_runs":[{"id":7,"name":"ci","status":"completed","conclusion":%q,"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","completed_at":"2026-11-01T00:00:00Z","html_url":"https://github.com/owner/repo/runs/7"}]}`, conclusion)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	prior := http.DefaultTransport
	http.DefaultTransport = githubPeerTransport{u, prior}
	defer func() { http.DefaultTransport = prior }()
	invokeInput := func(input string, args ...string) ([]byte, error) {
		t.Helper()
		var out, errOut bytes.Buffer
		err := run(context.Background(), append([]string{"-config", config, "github"}, args...), strings.NewReader(input), &out, &errOut)
		return out.Bytes(), err
	}
	invoke := func(op string) ([]byte, error) { return invokeInput("", op) }
	events := func(want int) {
		t.Helper()
		raw, err := invoke("events")
		var events []github.Observation
		if err != nil || json.Unmarshal(raw, &events) != nil || len(events) != want {
			t.Fatalf("events: %s %v", raw, err)
		}
	}
	for range 2 {
		if _, err = invoke("poll"); err != nil {
			t.Fatal(err)
		}
	}
	events(1)
	revision.Store(1)
	fail.Store(true)
	if _, err = invoke("poll"); err == nil || strings.Contains(err.Error(), "private provider") {
		t.Fatal("unsafe scan failure", err)
	}
	events(1)
	fail.Store(false)
	if _, err = invoke("poll"); err != nil {
		t.Fatal(err)
	}
	events(2)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "data", "state.db")+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"run", "main_input", "task"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("observation created unauthorized work", table, n, err)
		}
	}

	// Seed one existing owner Task. Rule binding, authenticated observation,
	// decision readback and close then use the real CLI and state adapter.
	if _, err = db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version)
 SELECT 'fixture-task','task:cli-rule',agent_id,agent_id,'Review PR','Review this exact PR','[]','active',1,1,1,1 FROM agent_config`); err != nil {
		t.Fatal(err)
	}
	raw, err := invokeInput(`{"task_ref":"task:cli-rule","task_epoch":1,"version":0,"mode":"observe","event_kind":"review_comment"}`, "bind")
	var binding github.TaskBinding
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Mode != "observe" || binding.TaskGoal != "Review this exact PR" || binding.Scope != "owner/repo#7" {
		t.Fatal("observe binding was not projected", string(raw), err)
	}
	revision.Store(2)
	for range 2 {
		if _, err = invoke("poll"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = invoke("decisions")
	var decisions []github.RuleDecision
	if err != nil || json.Unmarshal(raw, &decisions) != nil || len(decisions) != 1 ||
		!decisions[0].Matched || decisions[0].Mode != "observe" || decisions[0].TaskRef != "task:cli-rule" ||
		strings.Contains(string(raw), "requested change") {
		t.Fatal("decision query did not return one scoped receipt", string(raw), err)
	}
	raw, err = invokeInput("", "decisions", strconv.FormatInt(decisions[0].Sequence, 10))
	if err != nil || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatal("decision cursor did not advance", string(raw), err)
	}
	fail.Store(true)
	if _, err = invoke("poll"); err == nil {
		t.Fatal("test peer did not interrupt the watch")
	}
	raw, err = invokeInput(fmt.Sprintf(`{"version":%d}`, binding.Version), "unbind")
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Mode != "off" || binding.State != "stopped" {
		t.Fatal("interrupted watch blocked turning the rule off", string(raw), err)
	}
	fail.Store(false)
	revision.Store(3)
	if _, err = invoke("poll"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM github_rule_decision`).Scan(&count); err != nil || count != 1 {
		t.Fatal("disabled rule recorded a decision", count, err)
	}
	for _, fields := range []string{
		`"check_conclusion":"failure"`,
		`"mode":"observe","event_kind":"review","check_conclusion":"failure"`,
		`"mode":"observe","event_kind":"check_run","check_conclusion":"success"`,
		`"mode":"observe","event_kind":"check_run","check_conclusion":null`,
	} {
		input := fmt.Sprintf(`{"task_ref":"task:cli-rule","task_epoch":1,"version":%d,%s}`, binding.Version, fields)
		if raw, err = invokeInput(input, "bind"); err == nil || len(raw) != 0 {
			t.Fatal("invalid check rule accepted", fields, err)
		}
	}
	input := fmt.Sprintf(`{"task_ref":"task:cli-rule","task_epoch":1,"version":%d,"mode":"observe","event_kind":"check_run","check_conclusion":"failure"}`, binding.Version)
	raw, err = invokeInput(input, "bind")
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.CheckConclusion != "failure" {
		t.Fatal("CLI lost the configured check selector", string(raw), err)
	}
	for _, revision := range []int64{1, 2, 2} {
		checkRevision.Store(revision)
		if _, err = invoke("poll"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = invoke("decisions")
	if err != nil || json.Unmarshal(raw, &decisions) != nil || len(decisions) != 3 ||
		!decisions[0].Matched || decisions[0].ExpectedCheckConclusion != "failure" || decisions[0].ActualCheckConclusion != "failure" ||
		decisions[1].Matched || decisions[1].ActualCheckConclusion != "success" ||
		decisions[1].Reason != "check_conclusion_mismatch" || decisions[2].ActualCheckConclusion != "" {
		t.Fatal("CLI did not retain both exact check versions or duplicated a decision", string(raw), err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM run`).Scan(&count); err != nil || count != 0 {
		t.Fatal("observation mode created execution", count, err)
	}
}

func TestGitHubCLIControlPersistsWithoutExecutionReadiness(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEWARD_DATA_ROOT", filepath.Join(root, "data"))
	t.Setenv("GITHUB_OBSERVER_MISSING_TEST", "")
	config := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(config, []byte("version: 1\njavascript:\n  command: /missing/node\n  script: /missing/script.mjs\ngithub:\n  repository: owner/repo\n  pull_number: 7\n  credential_ref: env:GITHUB_OBSERVER_MISSING_TEST\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(operation string) ([]byte, error) {
		t.Helper()
		var out, errOut bytes.Buffer
		err := run(context.Background(), []string{"-config", config, "github", operation}, strings.NewReader(""), &out, &errOut)
		return out.Bytes(), err
	}
	status := func(want string) {
		t.Helper()
		raw, err := invoke("status")
		var v github.Status
		if err != nil || json.Unmarshal(raw, &v) != nil || v.State != want {
			t.Fatalf("status %s: %s %v", want, raw, err)
		}
	}
	status("never_checked")
	if raw, err := invoke("binding"); err != nil || !strings.Contains(string(raw), `"state":"unbound"`) {
		t.Fatal("binding control depended on execution runtime", string(raw), err)
	}
	if _, err := invoke("poll"); err == nil || !strings.Contains(err.Error(), "CREDENTIAL_MISSING") {
		t.Fatal("missing credential accepted", err)
	}
	status("interrupted")
	if _, err := invoke("stop"); err != nil {
		t.Fatal(err)
	}
	status("stopped")
	if _, err := invoke("poll"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatal("stopped watch revived", err)
	}
	if raw, err := invoke("events"); err != nil || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatal(string(raw), err)
	}
	if _, err := invoke("resume"); err != nil {
		t.Fatal(err)
	}
	status("never_checked")
	// Removing the source from the next process must not project its old scope
	// as active or leave its observations reachable through the current config.
	if err := os.WriteFile(config, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status("not_configured")
	if _, err := invoke("events"); err == nil {
		t.Fatal("removed source remained accessible")
	}
}

func TestGitHubCLISelectsOnlyExplicitPRWhenTwoWatchesAreConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEWARD_DATA_ROOT", filepath.Join(root, "data"))
	cfg := filepath.Join(root, "config.yaml")
	configuration := "version: 1\ngithub:\n  repository: Owner/Repo\n  pull_number: 7\n  credential_ref: env:GITHUB_PRIMARY\ngithub_watches:\n  - repository: owner/repo\n    pull_number: 8\n    credential_ref: env:GITHUB_SECONDARY\n"
	if err := os.WriteFile(cfg, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(scope, command string) (string, error) {
		var out, stderr bytes.Buffer
		args := []string{"-config", cfg}
		if scope != "" {
			args = append(args, "-github-scope", scope)
		}
		args = append(args, "github", command)
		err := run(context.Background(), args, strings.NewReader(""), &out, &stderr)
		return strings.TrimSpace(out.String()), err
	}
	if value, err := invoke("", "scopes"); err != nil || value != `["owner/repo#7","owner/repo#8"]` {
		t.Fatal(value, err)
	}
	for _, scope := range []string{"", "owner/repo#9"} {
		if _, err := invoke(scope, "stop"); err == nil {
			t.Fatal("ambiguous or unconfigured scope selected", scope)
		}
	}
	if _, err := invoke("owner/repo#7", "stop"); err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[string]string{"owner/repo#7": "stopped", "owner/repo#8": "never_checked"} {
		value, err := invoke(scope, "status")
		var status github.Status
		if err != nil || json.Unmarshal([]byte(value), &status) != nil || status.State != want {
			t.Fatal(scope, value, err)
		}
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(configuration, "pull_number: 8", "pull_number: 7", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("", "scopes"); err == nil {
		t.Fatal("duplicate canonical PR accepted")
	}
}
