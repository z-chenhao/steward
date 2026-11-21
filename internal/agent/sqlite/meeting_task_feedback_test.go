package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	appstools "github.com/z-chenhao/assistant/internal/plugin/connectedapps"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

type meetingWebCore struct {
	web.Core
	store *Store
}

func (c meetingWebCore) CheckChannel(ctx context.Context, channel, subject, destination string) error {
	return c.store.CheckChannel(ctx, channel, subject, destination)
}

// Keep the normal account link/check peer. Only the tool descriptor and Gmail
// read endpoint differ. Any accidental send still hits the existing send counter.
func meetingReadPeer(t *testing.T, s *Store) (*appsPeer, *atomic.Int32) {
	t.Helper()
	p := newAppsPeer(t, s)
	p.tool.Slug, p.tool.Name, p.tool.Description = "GMAIL_LIST_THREADS", "Find meeting threads", "List matching thread subjects"
	p.tool.Input = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)
	remote, err := url.Parse(p.service.Client.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	accounts := httputil.NewSingleHostReverseProxy(remote)
	reads := new(atomic.Int32)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/tools/" + p.tool.Slug:
			if req.Method != http.MethodGet || req.URL.Query().Get("version") != p.tool.Version {
				t.Error("invalid read descriptor query", req.URL)
			}
			_ = json.NewEncoder(w).Encode(p.tool)
		case "/tools/execute/" + p.tool.Slug:
			reads.Add(1)
			var body struct {
				Account   string            `json:"connected_account_id"`
				User      string            `json:"user_id"`
				Version   string            `json:"version"`
				Arguments map[string]string `json:"arguments"`
			}
			state, err := s.LoadApps(req.Context())
			if err != nil || req.Header.Get("x-api-key") != "composio-project-private" || req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&body) != nil || body.Account != "ca_work" || body.User != state.UserID || body.Version != p.tool.Version || len(body.Arguments) != 1 || body.Arguments["q"] != "subject:(Release review)" {
				t.Error("read exceeded reviewed request", body, err)
			}
			fmt.Fprint(w, `{"successful":true,"data":{"threads":[{"subject":"Release review","id":"thread-952"}]}}`)
		default:
			accounts.ServeHTTP(w, req)
		}
	}))
	t.Cleanup(peer.Close)
	p.service.Client.Endpoint = peer.URL
	return p, reads
}

func TestMeetingTaskResumesOnlyAfterOwnerConnectsAndAnswers(t *testing.T) {
	ctx := context.Background()
	s, r, a, model := childFixture(t, codemode.Definition())
	peer, reads := meetingReadPeer(t, s)
	a, host := appsPluginRuntime(t, s, r, a, peer.service)
	installFeedbackPlugin(t, s, r)
	base, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, appsDigest := selectAppsVersion(t, host, base.Revision, "1.0.2")
	appsPolicy, err := host.Store.Policy(ctx, "apps-owner-1.0.2")
	if err != nil {
		t.Fatal(err)
	}
	joinJourneyPlugin(t, host, base.Revision, "feedback-owner-fixture", "meeting-feedback-owner", "connected-apps", appsDigest, appsPolicy)
	// Account changes rebuild the available group. A Run captures its own copy
	// at creation just as it does under the production Apps refresh callback.
	peer.service.Changed = func(ctx context.Context) error {
		defs, err := appstools.Definitions(ctx, peer.service)
		if err != nil {
			return err
		}
		for i := range defs {
			defs[i].Spec.CodeModeOnly = true
		}
		return a.Registry.ReplaceGroup(appstools.Prefix, defs)
	}
	if _, err := s.BindChannel(ctx, "web", interaction.WebSubject, interaction.WebDestination, true, 0); err != nil {
		t.Fatal(err)
	}
	const anchor = "Prepare a briefing for the Release review meeting."
	const missing = "Gmail is missing for the Release review."
	const question = "Connect the work Gmail account in Settings. Return to this task and confirm if I may look for the Release review thread."
	const answer = "Continue this meeting task using work Gmail. Look for the Release review thread only."
	const thread = "thread-952"
	const briefing = "Release review briefing uses Gmail source " + thread
	// The same Root behavior runs before and after Connect. It inspects the
	// account tools that really exist in this Run's frozen CodeMode manifest.
	source := `const search = ALL_TOOLS.find(t => t.name.startsWith("mcp__apps__b__") && t.name.endsWith("__search_tools"));
if (!search) return "no-mail-account";
const result = await tools[search.name]({query: "list meeting threads"});
const op = result.items.find(d => d.slug === "GMAIL_LIST_THREADS");
return await tools[search.name.replace(/search_tools$/, "execute")]({
  tool_slug: op.slug, version: op.version, descriptor_hash: op.descriptor_hash,
  arguments_json: JSON.stringify({q: "subject:(Release review)"})
});`
	args, _ := json.Marshal(map[string]string{"source": source})
	model.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isRoot(req) {
			if last.Role != "tool" {
				return calls(codemode.ToolName, string(args)), nil
			}
			switch {
			case strings.Contains(last.Text, "no-mail-account"):
				return llm.GenerationResult{Text: missing}, nil
			case strings.Contains(last.Text, thread) && strings.Contains(last.Text, "Release review"):
				return llm.GenerationResult{Text: briefing}, nil
			default:
				return llm.GenerationResult{}, fmt.Errorf("unexpected worker receipt: %s", last.Text)
			}
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Task updated."}, nil
		}
		_, data, _ := strings.Cut(last.Text, "\n")
		switch {
		case strings.HasPrefix(last.Text, "Runtime task update") && strings.Contains(last.Text, missing):
			var report struct {
				Details task.UpdateDetails `json:"details"`
			}
			if err := json.Unmarshal([]byte(data), &report); err != nil {
				return llm.GenerationResult{}, err
			}
			return calls(task.ManageName, fmt.Sprintf(`{"operation":"request_input","taskRef":%q,"prompt":%q}`, report.Details.TaskRef, question)), nil
		case strings.HasPrefix(last.Text, "Response to a saved Task question"):
			var response struct {
				Ref      string `json:"task_ref"`
				Feedback string `json:"feedback_ref"`
				Answer   string `json:"answer"`
			}
			if err := json.Unmarshal([]byte(data), &response); err != nil || response.Answer != answer {
				return llm.GenerationResult{}, fmt.Errorf("lost owner consent %q: %v", response.Answer, err)
			}
			return calls(task.ManageName, fmt.Sprintf(`{"operation":"append","taskRef":%q,"feedbackRef":%q,"instruction":%q}`, response.Ref, response.Feedback, response.Answer)), nil
		case isFollowup(req) && strings.Contains(last.Text, briefing):
			return llm.GenerationResult{Text: briefing}, nil
		case isFollowup(req):
			return llm.GenerationResult{}, fmt.Errorf("reported meeting lacks its Gmail evidence")
		default:
			return calls(task.ManageName, fmt.Sprintf(`{"operation":"create","task":%q,"completionPolicy":"ongoing"}`, anchor)), nil
		}
	}
	first := mustSubmit(t, a, "prepare-meeting", anchor)
	mustDrain(t, a)
	var ref, oldRun, oldTools string
	if err := s.db.QueryRow(`SELECT t.task_ref,r.run_id,c.tools_json FROM task t JOIN run r USING(task_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.worker_position='root'`).Scan(&ref, &oldRun, &oldTools); err != nil {
		t.Fatal(err)
	}
	questions, err := s.TaskFeedbacks(ctx, ref, "")
	if err != nil || len(questions.Items) != 1 {
		t.Fatal("missing original task question", questions, err)
	}
	f := questions.Items[0]
	if f.Prompt != question || f.SourceMessageID != first.MessageID || strings.Contains(oldTools, appstools.Prefix) || reads.Load() != 0 {
		t.Fatal(f, oldRun)
	}
	roots := func() int { return scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root'`) }
	before := len(model.requests)
	server := web.Server{Core: meetingWebCore{store: s}, Apps: peer.service, Feedbacks: struct {
		*Store
		*Runs
	}{s, r}}
	post := func(path string, data url.Values, out any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080"+path, strings.NewReader(data.Encode()))
		req.RemoteAddr = "127.0.0.1:4200"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), out) != nil {
			t.Fatalf("owner POST %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	var link struct {
		ID  string `json:"connection_id"`
		URL string `json:"authorization_url"`
	}
	post("/apps/configure", url.Values{"action": {"connect"}, "toolkit": {"gmail"}, "alias": {"work"}}, &link)
	state, err := s.LoadApps(ctx)
	if err != nil || link.ID == "" || link.URL == "" || len(state.Connections) != 1 || state.Connections[0].ID != link.ID || state.Connections[0].Status != "pending" {
		t.Fatal("link did not identify the pending account", state, err)
	}
	var accounts json.RawMessage
	post("/apps/configure", url.Values{"action": {"check"}, "connection": {link.ID}}, &accounts)
	mustDrain(t, a)
	state, err = s.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].Status != "active" || len(model.requests) != before || roots() != 1 || reads.Load() != 0 || peer.executions.Load() != 0 || scalar(t, s, `SELECT count(*) FROM approval`) != 0 {
		t.Fatal("linking an account resumed or dispatched work", state, err)
	}
	respond := func(source string) string {
		t.Helper()
		var receipt struct {
			Accepted bool   `json:"accepted"`
			Message  string `json:"message_id"`
		}
		post("/task/feedback", url.Values{"source_id": {source}, "return_to": {"/tasks/" + ref}, "feedback_ref": {f.Ref}, "answer": {answer}}, &receipt)
		if !receipt.Accepted || receipt.Message == "" {
			t.Fatal("missing accepted feedback", receipt)
		}
		return receipt.Message
	}
	messageID := respond("first-tab")
	if respond("second-tab") != messageID || roots() != 1 {
		t.Fatal("duplicate feedback or direct root admission")
	}
	mustDrain(t, a)
	approvals, err := r.PendingApprovals(ctx)
	if err != nil || len(approvals) != 1 || !strings.Contains(approvals[0].Arguments, peer.tool.Slug) || scalar(t, s, `SELECT count(*) FROM approval a JOIN tool_invocation i USING(invocation_id) JOIN run r USING(run_id) JOIN task t USING(task_id) WHERE a.approval_id=? AND t.task_ref=?`, approvals[0].ID, ref) != 1 || roots() != 2 || reads.Load() != 0 || peer.executions.Load() != 0 {
		t.Fatalf("precise Gmail read must await its separate approval: %+v %v", approvals, err)
	}
	if err := r.Decide(ctx, idAgent(t, s), approvals[0].ID, approvals[0].Hash, "approved"); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if respond("after-result") != messageID {
		t.Fatal("accepted feedback lost after execution")
	}
	mustDrain(t, a)
	var preserved string
	if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, oldRun).Scan(&preserved); err != nil || preserved != oldTools {
		t.Fatal("old execution gained authority", err)
	}
	detail, err := s.TaskReader().DetailPage(ctx, ref, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for _, m := range detail.Messages {
		for _, part := range m.Content {
			if part.Text == briefing {
				delivered++
			}
		}
	}
	if delivered != 1 || reads.Load() != 1 || peer.executions.Load() != 0 || roots() != 2 || scalar(t, s, `SELECT count(*) FROM task`) != 1 {
		t.Fatal("meeting result was lost or repeated", delivered, reads.Load(), peer.executions.Load(), roots())
	}
	if scalar(t, s, `SELECT count(*) FROM task_instruction i JOIN task t USING(task_id) WHERE t.task_ref=? AND i.source_message_id=? AND i.instruction=?`, ref, messageID, answer) != 1 {
		t.Fatal("new round lost the exact task and owner response")
	}
}
