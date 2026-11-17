package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/task"
)

type mainPendingSettings struct {
	*attentionSettings
	pending []interaction.Approval
}

func (f mainPendingSettings) PendingApprovals(context.Context) ([]interaction.Approval, error) {
	return f.pending, nil
}

func TestAttentionOverflowMainApprovalsLocateActualChatAction(t *testing.T) {
	f := mainPendingSettings{attentionSettings: &attentionSettings{}}
	for i := 0; i < 51; i++ {
		f.pending = append(f.pending, interaction.Approval{ID: fmt.Sprintf("main-%02d", i), Status: "pending", Tool: "builtin__bash", Arguments: `{"command":"echo reviewed"}`, Hash: strings.Repeat("a", 64)})
	}
	// No Task reader exists: these Main approvals can only be found in Chat.
	s := &Server{Core: attentionCore{&updatesCore{allowed: true}}, Feedbacks: f}
	p := page{Status: Status{Readiness: "ready"}}
	s.loadAttention(context.Background(), &p, "", "", nil)
	if p.Attention == nil || p.Attention.Count != 50 || len(p.Attention.Approvals) != 50 || !p.Attention.More || p.Attention.ApprovalNext != "/#approval-main-50" || p.Attention.TasksNext != "" || p.Attention.Next != "" {
		t.Fatalf("Main approval overflow lost its destination: %+v", p.Attention)
	}
	var rendered bytes.Buffer
	if err := localizedLayout("en").ExecuteTemplate(&rendered, "tasks-attention", p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), `href="/#approval-main-50"`) || !strings.Contains(rendered.String(), ">50+</span>") {
		t.Fatal("bounded count or actual Chat approval link omitted", rendered.String())
	}
	// The existing Chat projection renders out-of-window pending actions, so
	// the destination is a real frozen approval even without a notification in
	// the latest conversation page or an associated Task.
	c := &updatesCore{allowed: true, approvals: f.pending}
	chat := &Server{Core: c}
	conversation := page{}
	if err := chat.loadConversation(context.Background(), &conversation); err != nil {
		t.Fatal(err)
	}
	rendered.Reset()
	if err := localizedLayout("en").ExecuteTemplate(&rendered, "approvals", conversation); err != nil || !strings.Contains(rendered.String(), `id="approval-main-50"`) {
		t.Fatal("Chat did not render destination approval", err, rendered.String())
	}
}

type attentionSettings struct {
	items  []task.Feedback
	last   interaction.InboundEvent
	answer string
}

func (f *attentionSettings) TaskFeedbacks(_ context.Context, ref, before string) (task.FeedbackPage, error) {
	page := task.FeedbackPage{Items: []task.Feedback{}}
	for _, v := range f.items {
		if ref == "" || v.TaskRef == ref {
			page.Items = append(page.Items, v)
		}
	}
	return page, nil
}
func (f *attentionSettings) RespondTaskFeedbackInbound(_ context.Context, e interaction.InboundEvent, ref, answer string) (agent.Receipt, error) {
	f.last, f.answer = e, answer
	f.items = nil
	return agent.Receipt{MessageID: "accepted"}, nil
}

type attentionCore struct{ *updatesCore }

func (c attentionCore) DescribeArtifact(context.Context, string) (artifact.Descriptor, error) {
	return artifact.Descriptor{Name: "report <script>.txt"}, nil
}

type unavailableAttentionCore struct{ attentionCore }

func (c unavailableAttentionCore) WebStatus(context.Context) (Status, error) {
	return Status{Readiness: "setup_required"}, nil
}

type failedWorkReader struct{ workspaceReader }

func (failedWorkReader) List(context.Context, task.ListQuery) (task.List, error) {
	return task.List{Items: []task.Entry{{
		Summary:  task.Summary{TaskRef: "task:visible", Title: "Open task", Status: "active"},
		Progress: task.Progress{Root: "failed"},
	}}}, nil
}

func TestAttentionKeepsWorkAndExcludesStartupConfiguration(t *testing.T) {
	core := attentionCore{&updatesCore{allowed: true}}
	s := &Server{Core: core, Feedbacks: &attentionSettings{}, Tasks: failedWorkReader{}}
	for _, readiness := range []string{"setup_required", "degraded", "ready"} {
		p := page{Status: Status{Readiness: readiness}}
		s.loadAttention(context.Background(), &p, "", "", nil)
		if p.Attention.Count != 1 || len(p.Attention.Issues) != 1 || p.Attention.Issues[0].Key != "task:visible" {
			t.Fatalf("readiness %s altered work attention: %+v", readiness, p.Attention)
		}
		var rendered bytes.Buffer
		if err := localizedLayout("zh-CN").ExecuteTemplate(&rendered, "tasks-attention", p); err != nil {
			t.Fatal(err)
		}
		if body := rendered.String(); !strings.Contains(body, "<h3>Open task</h3>") ||
			!strings.Contains(body, ">打开任务</a>") || strings.Contains(body, "Check your model connection") {
			t.Fatalf("task title or action used the wrong language: %s", body)
		}
	}
}

func TestWebFeedbackDisabledAnswerHasModelRecoveryRoute(t *testing.T) {
	f := &attentionSettings{items: []task.Feedback{{Ref: "feedback:one", TaskRef: "task:visible", Kind: "input", Prompt: "Which date?"}}}
	s := &Server{Core: unavailableAttentionCore{attentionCore{&updatesCore{allowed: true}}}, Feedbacks: f, Tasks: workspaceReader{}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/tasks/task:visible", nil)
	r.RemoteAddr = "127.0.0.1:4321"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `type="submit" disabled`) || !strings.Contains(w.Body.String(), `data-feedback-model-needed >`) || !strings.Contains(w.Body.String(), "Open model settings") {
		t.Fatal("disabled answer has no model recovery route", w.Code, w.Body.String())
	}
}

func TestWebFeedbackAttentionRendersExactEscapedSourceAndBoundSubmission(t *testing.T) {
	f := &attentionSettings{items: []task.Feedback{{Ref: "feedback:one", TaskRef: "task:visible", TaskTitle: "Finished report", SourceSequence: 7, Kind: "review", Prompt: "Check <script>revision A</script>", ArtifactRefs: []string{"artifact:exact"}}}}
	s := &Server{Core: attentionCore{&updatesCore{allowed: true, status: "idle"}}, Feedbacks: f, Tasks: workspaceReader{}}
	s.WebWorkbench = workbenchTestReader(t)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.RemoteAddr = "127.0.0.1:4321"
		s.ServeHTTP(w, r)
		return w
	}
	w := get("/tasks/attention")
	for _, value := range []string{"Needs your attention", "Finished report", "Review result", "&lt;script&gt;revision A&lt;/script&gt;", "report &lt;script&gt;.txt"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), value) {
			t.Fatal(value, w.Code, w.Body.String())
		}
	}
	detail := get("/tasks/task:visible")
	for _, value := range []string{"/task/feedback", `name="feedback_ref" value="feedback:one"`, "View related message", "?before=8", "Reviewing a result does not approve external actions."} {
		if !strings.Contains(detail.Body.String(), value) {
			t.Fatal(value, detail.Body.String())
		}
	}
	data := url.Values{"source_id": {"page-id"}, "feedback_ref": {"feedback:one"}, "answer": {"Keep revision A"}, "return_to": {"/tasks/task:visible"}}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/task/feedback", strings.NewReader(data.Encode()))
	r.RemoteAddr = "127.0.0.1:4321"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || f.last.Channel != "web" || f.last.Subject != Subject || f.last.Destination != Destination || f.answer != "Keep revision A" {
		t.Fatal(w.Code, w.Body.String(), f.last)
	}
	if strings.Contains(get("/tasks/attention").Body.String(), "feedback:one") {
		t.Fatal("answered review remains actionable")
	}
	core := s.Core.(attentionCore)
	core.allowed = false
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("revoked channel reached feedback", w.Code)
	}
}

func TestWebFeedbackRejectsLongUTF8AsKnownInvalidAndKeepsNativeDraft(t *testing.T) {
	const ref = "feedback:one"
	f := &attentionSettings{items: []task.Feedback{{Ref: ref, TaskRef: "task:visible", Kind: "input", Prompt: "Which date?"}}}
	s := &Server{Core: attentionCore{&updatesCore{allowed: true, status: "idle"}}, Feedbacks: f, Tasks: workspaceReader{}}
	s.WebWorkbench = workbenchTestReader(t)
	answer := strings.Repeat("中", 6000)
	for _, accept := range []string{"application/json", "text/html"} {
		data := url.Values{"source_id": {ref}, "feedback_ref": {ref}, "answer": {answer}, "return_to": {"/tasks/task:visible"}}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/task/feedback", strings.NewReader(data.Encode()))
		r.RemoteAddr = "127.0.0.1:4321"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 400 || f.answer != "" {
			t.Fatal("invalid answer reached feedback acceptance", w.Code)
		}
		if accept == "application/json" && !strings.Contains(w.Body.String(), "invalid_answer") {
			t.Fatal(w.Body.String())
		}
		if accept == "text/html" && (!strings.Contains(w.Body.String(), answer) || !strings.Contains(w.Body.String(), "too long or invalid")) {
			t.Fatal("native validation lost editable answer draft")
		}
	}
}

func TestAttentionInputFallbackDoesNotDuplicateStructuredQuestion(t *testing.T) {
	v := &attentionView{Feedbacks: []feedbackView{{Feedback: task.Feedback{TaskRef: "task:one", Kind: "input"}}}}
	entry := task.Entry{Summary: task.Summary{TaskRef: "task:one", Status: "active"}, Progress: task.Progress{Input: true}}
	addAttentionIssue(v, entry)
	if len(v.Issues) != 0 {
		t.Fatal("structured question duplicated input fallback")
	}
	entry.Progress.Unknown = true
	addAttentionIssue(v, entry)
	if len(v.Issues) != 1 {
		t.Fatal("independent unknown outcome hidden by question")
	}
}
