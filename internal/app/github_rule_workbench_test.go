package app

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Exercise the actual distributed workbench and GitHub owners together, through
// the same task page and forms as the browser. Only the remote PR is controlled.
func verifyGitHubRuleWorkbench(t *testing.T, a *App, carrier string, nextComment func()) {
	t.Helper()
	ctx := context.Background()
	if _, err := a.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMicro()
	if err := a.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version,completion_policy) VALUES('review-web','task:review-web',?,?,'Review the changes','Review the changes','[]','active',1,?,?,1,'ongoing')`, d.AgentID, d.AgentID, now, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h := a.Plugins()
	h.Catalog.Launcher = carrier
	srv := httptest.NewServer(&web.Server{Core: a, Tasks: a.store.TaskReader(), WebWorkbench: func(ctx context.Context, ref string) (contract.Presentation, string, bool, error) {
		return a.readWebWorkbenchWithHost(ctx, h, ref)
	}})
	defer srv.Close()
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	const path = "/tasks/task:review-web"
	get := func(expect string) url.Values {
		t.Helper()
		res, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 || !strings.Contains(string(raw), expect) || strings.Contains(string(raw), "PRIVATE review text") {
			t.Fatalf("rule page HTTP %d, missing %q or disclosed private observation", res.StatusCode, expect)
		}
		doc, err := html.Parse(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		attr := func(n *html.Node, name string) string {
			for _, v := range n.Attr {
				if v.Key == name {
					return v.Val
				}
			}
			return ""
		}
		for n := range doc.Descendants() {
			if n.DataAtom != atom.Form || attr(n, "action") != "/task/github-rule" {
				continue
			}
			f := url.Values{}
			for field := range n.Descendants() {
				if field.DataAtom == atom.Input && attr(field, "type") == "hidden" {
					f.Set(attr(field, "name"), attr(field, "value"))
				}
			}
			return f
		}
		t.Fatal("missing rule form")
		return nil
	}
	post := func(f url.Values, action, event string, want int) {
		t.Helper()
		copy := url.Values{}
		for k, v := range f {
			copy[k] = v
		}
		copy.Set("action", action)
		if event != "" {
			copy.Set("event", event)
		}
		res, err := client.PostForm(srv.URL+"/task/github-rule", copy)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		io.Copy(io.Discard, res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s HTTP %d want %d", action, res.StatusCode, want)
		}
		if want == 303 && res.Header.Get("Location") != path+"?github_scope=owner%2Frepo%237#github-rule" {
			t.Fatal("lost exact scope", res.Header.Get("Location"))
		}
	}
	original := get("Start observing")
	post(original, "enable", "", 409)
	post(original, "observe", "review_comment", 303)
	post(original, "observe", "review", 409)
	// A fragment-only GET can leave the browser on the same document and
	// keep showing old receipts. Pin the native refresh form to a new GET.
	get(`action="/tasks/task:review-web"><input type="hidden" name="github_scope" value="owner/repo#7"`)
	observing := get("Waiting for new events on this rule.")
	nextComment()
	if err := a.PollGitHubFor(ctx, "owner/repo#7"); err != nil {
		t.Fatal(err)
	}
	get("Event type matched")
	binding, err := a.GitHubTaskBindingFor(ctx, "owner/repo#7")
	if err != nil || binding.Mode != "observe" || binding.TaskRef != "task:review-web" {
		t.Fatal(binding, err)
	}
	var queued int
	if err := a.store.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM github_task_followup`).Scan(&queued)
	}); err != nil || queued != 0 {
		t.Fatal("observation queued work", queued, err)
	}
	post(observing, "enable", "", 303)
	auto := get("Automatic continuation enabled")
	post(observing, "stop", "", 409)
	auto.Set("epoch", "2")
	post(auto, "stop", "", 409)
	auto.Set("epoch", "1")
	post(auto, "stop", "", 303)
	get("Start observing")
	binding, err = a.GitHubTaskBindingFor(ctx, "owner/repo#7")
	if err != nil || binding.State != "stopped" {
		t.Fatal(binding, err)
	}
}
