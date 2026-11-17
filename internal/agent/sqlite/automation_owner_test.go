package sqlite

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
)

func TestOwnerScheduleConfigurationIsVersionedScopedAndReplaySafe(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	zero := int64(0)
	q := automation.OwnerConfiguration{Instruction: "Review primary sources", Type: "cron", Spec: "0 8 * * *", Timezone: "Asia/Shanghai", Enabled: true, Below: &zero}
	first, err := r.ConfigureAutomation(ctx, "web:create", q, now)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ConfigureAutomation(ctx, "web:create", q, now.AddDate(0, 0, 2))
	if err != nil || replay != first {
		t.Fatal("creation replay changed identity", replay, err)
	}
	q.Instruction = "Different request"
	if _, err = r.ConfigureAutomation(ctx, "web:create", q, now); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("receipt accepted different content", err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation`) != 1 || scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("saving dispatched work or duplicated schedule")
	}
	q.Ref, q.Version = first.Ref, first.Version
	q.Enabled = false
	edit, err := r.ConfigureAutomation(ctx, "web:disable", q, now)
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation WHERE enabled=0 AND next_fire_at IS NULL AND below_value=0`) != 1 {
		t.Fatal("zero threshold or reversible disable lost")
	}
	if _, err = r.ConfigureAutomation(ctx, "web:stale", q, now); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	q.Version = edit.Version
	q.Ref = "automation:other-owner"
	if _, err = s.db.Exec(`INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,created_at,updated_at,version) VALUES('automation:other-owner','other-owner','private','cron','0 8 * * *','UTC',0,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	q.Version = 1
	if _, err = r.ConfigureAutomation(ctx, "web:foreign", q, now); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("foreign owner changed", err)
	}
	q.Ref, q.Version = edit.Ref, edit.Version
	if _, err = s.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,history_floor,updated_at) SELECT agent_id,1,0,100 FROM agent_config WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ConfigureAutomation(ctx, "web:revoked", q, now); !errors.Is(err, agent.ErrConflict) {
		t.Fatal("revoked intent renewed", err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_request WHERE request_key LIKE 'owner-config:%'`) != 2 {
		t.Fatal("rejected configuration persisted a receipt")
	}
}

type ownerHTTPSettings struct {
	r   *Runs
	s   *Store
	now time.Time
}

func (v ownerHTTPSettings) ScheduleDefaults(ctx context.Context) (automation.EditorDefaults, error) {
	return v.r.OwnerScheduleDefaults(ctx)
}

func (v ownerHTTPSettings) Schedule(ctx context.Context, ref string) (task.ScheduledWork, error) {
	return v.s.TaskReader().ScheduledDetail(ctx, ref)
}
func (v ownerHTTPSettings) ConfigureSchedule(ctx context.Context, key string, q automation.OwnerConfiguration) (automation.OwnerReceipt, error) {
	return v.r.ConfigureAutomation(ctx, "web:"+key, q, v.now)
}

type ownerHTTPCore struct{ web.Core }

func (ownerHTTPCore) CheckChannel(context.Context, string, string, string) error { return nil }
func (ownerHTTPCore) WebStatus(context.Context) (web.Status, error) {
	return web.Status{Readiness: "ready", Status: "idle"}, nil
}

func TestOwnerScheduleHTTPRetryReplaysCreateAndExactOnceEdit(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	original := "2030-01-03T08:30:45.123456789+08:00"
	seed, err := r.ConfigureAutomation(ctx, "seed", automation.OwnerConfiguration{Instruction: "Original", Type: "at", Spec: original, Timezone: "Asia/Shanghai", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	server := &web.Server{Core: ownerHTTPCore{}, Schedules: ownerHTTPSettings{r, s, now}}
	post := func(v url.Values) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1:8080/schedule/configure", strings.NewReader(v.Encode()))
		request.RemoteAddr = "127.0.0.1:9000"
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	v := url.Values{"source_id": {strings.Repeat("a", 32)}, "ref": {seed.Ref}, "version": {"1"}, "instruction": {"Edited instructions"}, "frequency": {"once"}, "date": {"2030-01-03"}, "time": {"08:30"}, "timezone": {"Asia/Shanghai"}, "original_spec": {original}, "original_timezone": {"Asia/Shanghai"}, "threshold": {"0"}}
	first, retry := post(v), post(v)
	if first.Code != 303 || retry.Code != 303 || first.Header().Get("Location") != retry.Header().Get("Location") {
		t.Fatal("lost acknowledgement retry did not replay", first.Code, retry.Code, first.Body.String(), retry.Body.String())
	}
	var spec string
	var version int
	s.db.QueryRow(`SELECT trigger_spec,version FROM automation WHERE automation_id=?`, seed.Ref).Scan(&spec, &version)
	if spec != original || version != 2 || scalar(t, s, `SELECT count(*) FROM automation_request WHERE request_key='owner-config:web:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`) != 1 {
		t.Fatal("retry rounded instant or applied edit twice", spec, version)
	}
	v.Set("instruction", "Different body")
	if changed := post(v); changed.Code != 409 {
		t.Fatal("same key accepted different body", changed.Code)
	}
	v.Set("source_id", strings.Repeat("b", 32))
	v.Set("version", "2")
	v.Set("original_spec", "2030-01-03T08:30:45.999+08:00")
	if forged := post(v); forged.Code != 409 {
		t.Fatal("forged original instant accepted", forged.Code)
	}
	v = url.Values{"source_id": {strings.Repeat("c", 32)}, "ref": {""}, "version": {"0"}, "instruction": {"New repeating arrangement"}, "frequency": {"daily"}, "time": {"08:30"}, "timezone": {"Asia/Shanghai"}, "enabled": {"yes"}}
	first, retry = post(v), post(v)
	if first.Code != 303 || retry.Code != 303 || first.Header().Get("Location") != retry.Header().Get("Location") || scalar(t, s, `SELECT count(*) FROM automation`) != 2 {
		t.Fatal("create HTTP retry duplicated schedule", first.Code, retry.Code)
	}
}

func TestOwnerSchedulePausePreservesAdmittedWorkAndInvalidatesPendingFires(t *testing.T) {
	s, r, a, p := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	below := int64(5)
	q := automation.OwnerConfiguration{Instruction: "Observe a fixed value", Type: "cron", Spec: "0 8 * * *", Timezone: "UTC", Enabled: true, Below: &below}
	receipt, err := r.ConfigureAutomation(ctx, "create", q, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	var frozen string
	if err = s.db.QueryRow(`SELECT checkpoint_json||model_snapshot_id||capability_snapshot_id FROM run WHERE kind='worker'`).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	q.Ref = receipt.Ref
	if err = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, q.Ref).Scan(&q.Version); err != nil {
		t.Fatal(err)
	}
	q.Enabled = false
	receipt, err = r.ConfigureAutomation(ctx, "pause", q, now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var after string
	s.db.QueryRow(`SELECT checkpoint_json||model_snapshot_id||capability_snapshot_id FROM run WHERE kind='worker'`).Scan(&after)
	if after != frozen || scalar(t, s, `SELECT count(*) FROM task WHERE status='active'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE status='queued'`) != 1 || scalar(t, s, `SELECT count(*) FROM model_generation`) != 0 || scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='admitted' AND instruction='Observe a fixed value'`) != 1 {
		t.Fatal("pause rewrote or ran admitted work")
	}
	// Existing observation eligibility is deliberately current-version-only:
	// an admitted old monitor cannot notify after its schedule was disabled.
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role != "tool" {
			return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "observe", Name: automation.ObserveName, Arguments: `{"value":1}`}}}, nil
		}
		return llm.GenerationResult{Text: "Observation was not admitted"}, nil
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE observed_value IS NOT NULL OR notify=1`) != 0 {
		t.Fatal("old schedule observation notified")
	}
	q.Enabled = true
	q.Version = receipt.Version
	receipt, err = r.ConfigureAutomation(ctx, "resume", q, now.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ScheduleAutomations(ctx, now.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT version FROM automation WHERE automation_id=?`, q.Ref).Scan(&q.Version); err != nil {
		t.Fatal(err)
	}
	q.Instruction = "New future instructions"
	if _, err = r.ConfigureAutomation(ctx, "edit-pending", q, now.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if err = r.admitAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE state='skipped'`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE kind='worker'`) != 1 {
		t.Fatal("old pending fire dispatched after edit")
	}
}

func TestOwnerCanDisableExhaustedOnceButCannotResumePastOccurrence(t *testing.T) {
	_, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	q := automation.OwnerConfiguration{Instruction: "One occurrence", Type: "at", Spec: now.Add(time.Hour).Format(time.RFC3339), Timezone: "UTC", Enabled: true}
	v, err := r.ConfigureAutomation(ctx, "once", q, now)
	if err != nil {
		t.Fatal(err)
	}
	q.Ref, q.Version, q.Enabled = v.Ref, v.Version, false
	v, err = r.ConfigureAutomation(ctx, "disable-old-once", q, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal("past once could not be disabled", err)
	}
	q.Version, q.Enabled = v.Version, true
	if _, err = r.ConfigureAutomation(ctx, "resume-old-once", q, now.Add(2*time.Hour)); !errors.Is(err, automation.ErrInvalid) {
		t.Fatal("past once resumed", err)
	}
}
