package sqlite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (v ownerHTTPSettings) TrialSchedule(ctx context.Context, key, ref string, version int64) (string, error) {
	return v.r.TrialAutomation(ctx, key, ref, version)
}

func TestOwnerScheduleTrialIsolatedThroughRootAndThreshold(t *testing.T) {
	s, r, a, p := automationFixture(t)
	ctx := t.Context()
	if _, err := s.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindChannel(ctx, "feishu", "tenant:app:owner", "bound-chat", true, 0); err != nil {
		t.Fatal(err)
	}
	method := installScheduleOwnerMethod(t, s, r)
	now := time.Now().UTC()
	below := int64(12)
	plan, err := r.ConfigureAutomation(ctx, "source", automation.OwnerConfiguration{Instruction: "Check the fixture price", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true, Below: &below}, now)
	if err != nil {
		t.Fatal(err)
	}
	r.SetTestAutomationClock(now)
	type state struct {
		next, version, threshold, last int64
		entered                        bool
	}
	read := func() state {
		t.Helper()
		var got state
		if err := s.db.QueryRow(`SELECT next_fire_at,version,below_value,last_observed_fire,is_below FROM automation WHERE automation_id=?`, plan.Ref).Scan(&got.next, &got.version, &got.threshold, &got.last, &got.entered); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := read()
	config := ownerHTTPSettings{r: r, s: s, now: now}
	server := web.Server{Schedules: config, Core: ownerHTTPCore{}}
	key := strings.Repeat("a", 32)
	submit := func() string {
		form := url.Values{"ref": {plan.Ref}, "version": {"1"}, "source_id": {key}}
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/schedule/trial", strings.NewReader(form.Encode()))
		req.RemoteAddr = "127.0.0.1:30111"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("trial HTTP %d: %s", w.Code, w.Body.String())
		}
		return w.Header().Get("Location")
	}
	page := submit()
	if !strings.HasPrefix(page, "/tasks/task:") || submit() != page {
		t.Fatal("trial receipt changed", page)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) && req.Messages[len(req.Messages)-1].Role != "tool" {
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "price", Name: automation.ObserveName, Arguments: fmtValue(9)}}}, nil
		}
		return llm.GenerationResult{Text: "The trial observed nine."}, nil
	}
	mustDrain(t, a)
	mustDrain(t, a)
	if before != read() || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE automation_id=?`, plan.Ref) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE automation_id LIKE 'trial:%' AND observed_value=9`) != 1 {
		t.Fatal("trial observation missing or original changed")
	}
	if scalar(t, s, `SELECT count(*) FROM task WHERE task_ref=? AND completion_policy='once' AND status='completed'`, strings.TrimPrefix(page, "/tasks/")) != 1 || scalar(t, s, `SELECT count(*) FROM worker_result WHERE content='The trial observed nine.'`) != 1 || scalar(t, s, `SELECT count(*) FROM task_update`) != 1 || scalar(t, s, `SELECT count(*) FROM delivery_request WHERE channel='feishu'`) != 0 {
		t.Fatal("trial result or notification isolation lost")
	}
	var rootID string
	if err = s.db.QueryRow(`SELECT root_run_id FROM automation_fire WHERE automation_id LIKE 'trial:%'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	execution, err := s.TaskReader().Execution(ctx, rootID)
	if err != nil || len(execution.Messages) != 1 || len(execution.Messages[0].Content) == 0 || execution.Messages[0].Content[0].Text != "The trial observed nine." {
		t.Fatalf("trial result unavailable at its execution: %+v %v", execution, err)
	}
	listing, _, err := s.TaskReader().ScheduledPage(ctx, "", "")
	if err != nil || len(listing) != 1 || listing[0].Ref != plan.Ref {
		t.Fatal(listing, err)
	}
	if _, err = r.TrialAutomation(ctx, strings.Repeat("b", 32), plan.Ref, 2); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("stale version", err)
	}
	if err = method.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := r.TrialAutomation(ctx, key, plan.Ref, 1); err != nil || "/tasks/"+got != page {
		t.Fatal("lost acknowledged trial after stop", got, err)
	}
	if _, err = r.TrialAutomation(ctx, strings.Repeat("c", 32), plan.Ref, 1); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("new trial after stop", err)
	}
}

func TestOwnerScheduleTrialApprovalsBelongToSeparateRuns(t *testing.T) {
	s, r, a, p := automationFixture(t)
	ctx := t.Context()
	if _, err := s.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	_ = installScheduleOwnerMethod(t, s, r)
	count := 0
	effect := tool.Definition{Spec: tool.Spec{Name: "mcp__fixture__external_publish", Version: 1, InputSchema: `{"type":"object","properties":{},"additionalProperties":false}`, Permission: "external_effect", ReplayPolicy: "never", Risk: "high", Provider: "fixture", OutputBytes: 1024, TimeoutMS: 10000, ConflictPolicy: "none"}, Invoke: func(context.Context, tool.Request) tool.Outcome {
		count++
		return tool.Outcome{Result: tool.Result{Text: "done"}}
	}}
	defs := []tool.Definition{effect}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry, a.Registry = registry, registry
	now := time.Now().UTC()
	plan, err := r.ConfigureAutomation(ctx, "source", automation.OwnerConfiguration{Instruction: "Request a fixture publication", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	r.SetTestAutomationClock(now)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) && req.Messages[len(req.Messages)-1].Role != "tool" {
			return calls(effect.Spec.Name, `{}`), nil
		}
		return llm.GenerationResult{Text: "Publication decision recorded"}, nil
	}
	for i, decision := range []string{"denied", "approved"} {
		ref, err := r.TrialAutomation(ctx, strings.Repeat([]string{"1", "2"}[i], 32), plan.Ref, plan.Version)
		if err != nil {
			t.Fatal(err)
		}
		mustDrain(t, a)
		var id, hash string
		if err = s.db.QueryRow(`SELECT a.approval_id,a.frozen_arguments_hash FROM approval a JOIN tool_invocation i USING(invocation_id) JOIN run r USING(run_id) JOIN task t USING(task_id) WHERE t.task_ref=? AND a.status='pending'`, ref).Scan(&id, &hash); err != nil || count != 0 {
			t.Fatal("trial skipped owner approval", err, count)
		}
		if err = r.Decide(ctx, idAgent(t, s), id, hash, decision); err != nil {
			t.Fatal(err)
		}
		mustDrain(t, a)
		if count != i {
			t.Fatal("unexpected effect count", count)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE automation_id=?`, plan.Ref) != 0 {
		t.Fatal("a trial approval fired the original plan")
	}
}
