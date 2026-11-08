package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/task"
)

type workspaceReader struct{ err error }

func (f workspaceReader) List(context.Context, task.ListQuery) (task.List, error) {
	return task.List{Items: []task.Entry{{Summary: task.Summary{TaskRef: "task:visible", Title: "Review report", Status: "active"}, Stage: "Working", CompletionPolicy: "ongoing"}}}, f.err
}
func (f workspaceReader) Executions(context.Context, task.ListQuery) (task.ExecutionList, error) {
	return task.ExecutionList{Items: []task.Execution{{ID: "owned-root", TaskRef: "task:visible", Title: "Review report", Status: "running", CompletionPolicy: "ongoing"}}}, f.err
}
func (f workspaceReader) Execution(context.Context, string) (task.ExecutionDetail, error) {
	return task.ExecutionDetail{Execution: task.Execution{ID: "owned-root", TaskRef: "task:visible", Title: "Review report", Status: "completed", Epoch: 1}, Description: "Prepare a review"}, f.err
}

func (f workspaceReader) Detail(context.Context, string, string) (task.Detail, error) {
	return task.Detail{Entry: task.Entry{Summary: task.Summary{TaskRef: "task:visible", Title: "Review <img src=x>", GoalSummary: "Prepare a review", Status: "active"}, Stage: "Working", CompletionPolicy: "ongoing", Progress: task.Progress{Running: 1, Succeeded: 2, Waiting: 1}}, SourceSequence: 8, Instructions: []task.Instruction{{Text: "Use the primary sources", At: "2026-11-03T00:00:00Z", Epoch: 1}}}, f.err
}

func (f workspaceReader) Scheduled(context.Context, string) ([]task.ScheduledWork, error) {
	return []task.ScheduledWork{{Schedule: task.Schedule{Ref: "automation:next", Instruction: "Prepare tomorrow <script>", Type: "cron", Spec: "0 8 * * *", Timezone: "Asia/Shanghai", Enabled: true, NextAt: "2026-11-04T00:00:00Z"}}}, f.err
}

type unavailableScheduleReader struct{ workspaceReader }

type executionWorkspaceReader struct {
	workspaceReader
	value task.ExecutionDetail
}

func (f executionWorkspaceReader) Execution(context.Context, string) (task.ExecutionDetail, error) {
	return f.value, nil
}

func (f executionWorkspaceReader) Executions(context.Context, task.ListQuery) (task.ExecutionList, error) {
	return task.ExecutionList{Items: []task.Execution{f.value.Execution}}, nil
}

func TestExecutionDetailPrioritizesDeliveryAndRefreshesRunFacts(t *testing.T) {
	f := executionWorkspaceReader{value: task.ExecutionDetail{
		Execution:   task.Execution{ID: "owned-root", TaskRef: "task:visible", Title: "Review report", Status: "running", Epoch: 2, NeedsAttention: true},
		Description: "Long instructions <img src=x>", ProgressSummary: "Comparing primary sources",
		RecentRuns: []task.Execution{{ID: "owned-root", Epoch: 2, Status: "running"}, {ID: "older-root", Epoch: 1, Status: "completed"}},
	}}
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Tasks: f}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.RemoteAddr = "127.0.0.1:4321"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := get("/tasks/executions/owned-root")
	markup := w.Body.String()
	if w.Code != 200 || strings.Index(markup, `class="execution-result"`) > strings.Index(markup, `class="execution-instructions"`) || !strings.Contains(markup, "Long instructions &lt;img") {
		t.Fatal("delivery does not lead or instructions escaped incorrectly", w.Code)
	}
	if strings.Contains(markup, `class="execution-instructions" open`) || strings.Contains(markup, `action="/task/`) || !strings.Contains(markup, "Work is in progress.") {
		t.Fatal("execution detail changed configuration authority or hid its live state")
	}
	if !strings.Contains(markup, `href="/tasks/task:visible">Needs your attention</a>`) {
		t.Fatal("read-only inspection lost the native attention action")
	}
	list := get("/tasks")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `data-execution-open href="/tasks/executions/owned-root"`) || strings.Contains(list.Body.String(), `class="task-list-title" href="/tasks/task:visible"`) {
		t.Fatal("attention row bypassed execution inspection", list.Code, list.Body.String())
	}
	for _, status := range []string{"running", "queued", "waiting", "failed", "cancelled", "completed"} {
		f.value.Status = status
		s.Tasks = f
		w = get("/api/updates?view=/tasks/executions/owned-root")
		var update struct{ Regions map[string]string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &update) != nil || len(update.Regions) != 5 {
			t.Fatal("execution live fragments unavailable", status, w.Code, w.Body.String())
		}
		if !strings.Contains(update.Regions["execution-header"], `data-status="`+status+`"`) || !strings.Contains(update.Regions["execution-side"], "/tasks/executions/older-root") || !strings.Contains(update.Regions["execution-progress"], "Comparing primary sources") || update.Regions["task-messages"] == "" {
			t.Fatal("live execution lost status, history, progress or empty result", status, update)
		}
	}
	f.value.Messages = []conversation.Message{{Role: "assistant", Sequence: 1, Content: conversation.Text("Approved Main delivery")}}
	f.value.RecentRuns = f.value.RecentRuns[:1]
	s.Tasks = f
	w = get("/tasks/executions/owned-root")
	if !strings.Contains(w.Body.String(), "Approved Main delivery") || strings.Contains(w.Body.String(), "No result has been delivered") {
		t.Fatal("Main delivery did not replace the empty state")
	}
	if strings.Contains(w.Body.String(), `class="execution-history"`) || strings.Contains(w.Body.String(), "Comparing primary sources") {
		t.Fatal("completed delivery repeated a single run or stale progress")
	}
	for _, label := range []string{`class="execution-instructions"`, `class="execution-metadata"`} {
		if !strings.Contains(w.Body.String(), label) || strings.Contains(w.Body.String(), label+` open`) {
			t.Fatal("secondary execution content must start collapsed", label)
		}
	}
}

func TestTaskPageCountMatchesRenderedRows(t *testing.T) {
	v := &taskView{
		Configurations: true,
		Scheduled:      []task.ScheduledWork{{}},
		List: task.List{Items: []task.Entry{
			{Schedule: &task.Schedule{}}, // This entry is already shown as a schedule.
			{},
		}},
		Executions: task.ExecutionList{Items: []task.Execution{{}, {}, {}}},
	}
	if taskPageCount(v) != 2 {
		t.Fatal("configuration count includes a hidden scheduled entry")
	}
	v.Configurations = false
	if taskPageCount(v) != 3 {
		t.Fatal("execution count included configurations")
	}
}

func (unavailableScheduleReader) Scheduled(context.Context, string) ([]task.ScheduledWork, error) {
	return nil, errors.New("PRIVATE_SCHEDULE_ERROR")
}

func TestTaskPagesAreAuthenticatedReadOnlyAndErrorsStayPrivate(t *testing.T) {
	s := &Server{Config: Config{TokenRef: "credential:test"}, Core: &updatesCore{allowed: true, status: "idle"}, Tasks: workspaceReader{}, Secrets: fakeSecrets{}}
	request := func(path string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		if auth {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/tasks", "/tasks/configurations", "/tasks/executions/owned-root", "/tasks/task:visible"} {
		if w := request(path, false); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
		if w := request(path, true); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	list := request("/tasks/configurations", true)
	for _, text := range []string{"Scheduled work", "Prepare tomorrow &lt;script&gt;", "Created", "Updated", "Enabled"} {
		if !strings.Contains(list.Body.String(), text) {
			t.Fatal("missing saved schedule", text)
		}
	}
	if strings.Contains(list.Body.String(), "/tasks/automation:") {
		t.Fatal("schedule became an invented task")
	}
	for _, view := range []string{"closed", "attention"} {
		w := request("/tasks?view="+view, true)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Prepare tomorrow") || strings.Contains(w.Body.String(), "<h2>Scheduled work</h2>") {
			t.Fatal("schedule escaped task view", view, w.Code)
		}
	}
	s.Tasks = unavailableScheduleReader{}
	unavailable := request("/tasks/configurations", true)
	if unavailable.Code != 200 || !strings.Contains(unavailable.Body.String(), "Schedules are temporarily unavailable") || !strings.Contains(unavailable.Body.String(), "Review report") || strings.Contains(unavailable.Body.String(), "PRIVATE_SCHEDULE_ERROR") {
		t.Fatal("schedule read masked tasks or leaked diagnostics", unavailable.Code, unavailable.Body.String())
	}
	s.Tasks = workspaceReader{}
	w := request("/tasks/task:visible", true)
	for _, text := range []string{"Review &lt;img src=x&gt;", "Continue this task", "before=9", "Related updates from Ghost", "Delegated work · 4 total", "Completed / failed", "Running / queued / waiting", "Planned objective", "Ongoing task", "Accepted instructions", "Use the primary sources", "Delegated work"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatal("missing task flow", text)
		}
	}
	if strings.Contains(w.Body.String(), "<img src=x") {
		t.Fatal("task title executed")
	}
	if w := request("/tasks?view=failed", true); w.Code != 400 {
		t.Fatal("invented Task state accepted", w.Code)
	}
	s.Tasks = workspaceReader{err: task.ErrNotFound}
	if w := request("/tasks/task:foreign", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
	s.Tasks = workspaceReader{err: errors.New("PRIVATE_SQL_ERROR")}
	if w := request("/tasks", true); w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE_SQL_ERROR") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTaskSidebarMatchesListAndDetailCategories(t *testing.T) {
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Tasks: workspaceReader{}}
	for _, test := range []struct{ path, selected string }{
		{"/tasks?view=closed&q=review", "/tasks"},
		{"/tasks/configurations", "/tasks/configurations"},
		{"/tasks/executions/owned-root", "/tasks"},
		{"/tasks/task:visible", "/tasks/configurations"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+test.path, nil)
		r.RemoteAddr = "127.0.0.1:4321"
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(test.path, w.Code)
		}
		markup := w.Body.String()
		_, sidebar, ok := strings.Cut(markup, `<aside class="section-sidebar" aria-label="Task navigation">`)
		if !ok || !strings.Contains(markup, `<body class="has-section-navigation`) {
			t.Fatal("Task page did not use the shared two-rail shell", test.path)
		}
		sidebar, _, _ = strings.Cut(sidebar, "</aside>")
		if !strings.Contains(sidebar, `href="`+test.selected+`" aria-current="page"`) || strings.Count(sidebar, `aria-current="page"`) != 1 {
			t.Fatal("detail lost its parent category", test.path, sidebar)
		}
		if strings.Contains(markup, `class="section-tabs" aria-label="Task sections"`) {
			t.Fatal("duplicate in-page category tabs", test.path)
		}
	}
}

func TestExecutionFiltersDefaultToAllAndPreserveIndependentAttention(t *testing.T) {
	for _, path := range []string{"/tasks", "/tasks?view="} {
		q, err := tasksQuery(httptest.NewRequest("GET", path, nil))
		if err != nil || q.View != "all" {
			t.Fatal(q, err)
		}
	}
	q, err := tasksQuery(httptest.NewRequest("GET", "/tasks?view=closed&needs_me=1&q=report", nil))
	if err != nil || !q.NeedsMe || q.View != "closed" {
		t.Fatal(q, err)
	}
	target := taskListURL(&taskView{Query: q}, "cursor", "next")
	if !strings.Contains(target, "needs_me=1") || !strings.Contains(target, "view=closed") || !strings.Contains(target, "q=report") {
		t.Fatal(target)
	}
	for _, path := range []string{"/tasks?needs_me=no", "/tasks?needs_me=1&needs_me=1"} {
		if _, err := tasksQuery(httptest.NewRequest("GET", path, nil)); err == nil {
			t.Fatal("invalid filter accepted", path)
		}
	}
}
