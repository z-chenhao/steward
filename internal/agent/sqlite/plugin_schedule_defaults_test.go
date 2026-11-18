package sqlite

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScheduleDefaultsActualHTTPUpgradeRetainsOldEditorWhileStopped(t *testing.T) {
	s, r, _, provider := automationFixture(t)
	ctx := context.Background()
	h := installScheduleOwnerMethod(t, s, r)
	firstGraph, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	original, err := r.ConfigureAutomation(ctx, "defaults-original", automation.OwnerConfiguration{Instruction: "Original editor recipe", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	installScheduleMethodVersion(t, s, r, "1.0.2", 4)
	server := &web.Server{Core: ownerHTTPCore{}, Schedules: ownerHTTPSettings{r, s, now}}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		req.RemoteAddr = "127.0.0.1:4321"
		result := httptest.NewRecorder()
		server.ServeHTTP(result, req)
		return result
	}
	// Public draft metadata must use the actual new isolated implementation.
	response := get("/tasks/schedules/new")
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, `value="09:15"`) || !strings.Contains(body, `value="Asia/Shanghai"`) || !strings.Contains(body, `value="weekdays" selected`) {
		t.Fatal("new form used native/default-old recipe", response.Code)
	}
	second, err := r.ConfigureAutomation(ctx, "defaults-upgraded", automation.OwnerConfiguration{Instruction: "Upgraded editor recipe", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	current, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	activateScheduleMethod(t, h, "defaults-exact-rollback", current.Revision, firstGraph.Revision, 2)
	response = get("/tasks/schedules/new")
	body = response.Body.String()
	if response.Code != 200 || !strings.Contains(body, `value="08:00"`) || !strings.Contains(body, `value="UTC"`) || !strings.Contains(body, `value="daily" selected`) {
		t.Fatal("exact rollback did not restore original draft policy", response.Code)
	}
	retained, err := s.TaskReader().ScheduledDetail(ctx, second.Ref)
	if err != nil || retained.Presentation == nil || retained.Presentation.EditorDefaults == nil || retained.Presentation.EditorDefaults.Clock != "09:15" {
		t.Fatal("rollback rewrote upgraded accepted recipe", retained, err)
	}
	if err = h.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	// First dependent old-record read does not start the stopped method.
	old, err := s.TaskReader().ScheduledDetail(ctx, original.Ref)
	if err != nil || old.Presentation == nil || old.Presentation.EditorDefaults == nil || old.Presentation.EditorDefaults.Clock != "08:00" || old.Presentation.EditorDefaults.Frequency != "daily" {
		t.Fatal("upgrade rewrote old recipe", old, err)
	}
	response = get("/tasks/schedules/" + original.Ref)
	if response.Code != 200 {
		t.Fatal("opening existing editor required stopped producer", response.Code)
	}
	if _, err = r.OwnerScheduleDefaults(ctx); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("new form stopped metadata fell back to native", err)
	}
	if err = h.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "defaults-deactivate", selected.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.OwnerScheduleDefaults(ctx); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("withdrawn defaults restored native recipe", err)
	}
	if response = get("/tasks/schedules/" + original.Ref); response.Code != 200 || len(provider.requests) != 0 || scalar(t, s, `SELECT count(*) FROM task`) != 0 {
		t.Fatal("old draft could not be read or dispatched work", response.Code)
	}
}

func TestScheduleManagedDefaultFactsCannotDisappearIntoLegacy(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	installScheduleOwnerMethod(t, s, r)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	receipt, err := r.ConfigureAutomation(ctx, "defaults-facts-required", automation.OwnerConfiguration{Instruction: "Retain recipe", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE automation SET presentation_json=json_remove(presentation_json,'$.editor_defaults'),schedule_version=schedule_version+1,version=version+1 WHERE automation_id=?`, receipt.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskReader().ScheduledDetail(ctx, receipt.Ref); !errors.Is(err, task.ErrInvalid) {
		t.Fatal("managed defaults silently recovered old recipe", err)
	}
}

func TestScheduleRejectsUnreachableThresholdAtAdmission(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := automationFixture(t)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	zero, one := int64(0), int64(1)
	q := automation.OwnerConfiguration{Instruction: "Check remaining stock", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Below: &zero, Enabled: true}
	old, err := r.ConfigureAutomation(ctx, "old-zero", q, now)
	if err != nil {
		t.Fatal(err)
	}
	installScheduleOwnerMethod(t, s, r)
	if replay, err := r.ConfigureAutomation(ctx, "old-zero", q, now); err != nil || replay != old {
		t.Fatal("historical receipt lost", replay, err)
	}
	var minimum automation.ThresholdMinimumError
	if _, err = r.ConfigureAutomation(ctx, "new-zero", q, now); !errors.As(err, &minimum) || minimum.Minimum != 1 {
		t.Fatal("owner accepted unreachable threshold", err)
	}
	cli := automation.Request{Operation: "create", Instruction: q.Instruction, Type: q.Type, Spec: q.Spec, Timezone: q.Timezone, Below: &zero}
	if _, err = r.ManageAutomation(ctx, "cli-zero", cli, now); !errors.As(err, &minimum) {
		t.Fatal("CLI accepted unreachable threshold", err)
	}
	r.SetTestAutomationClock(now)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Use one to detect zero."}, nil
		}
		return calls(automation.ManageName, `{"operation":"create","instruction":"Check zero","triggerType":"cron","triggerSpec":"* * * * *","timezone":"UTC","below":0}`), nil
	}
	mustSubmit(t, a, "main-zero", "Notify me when stock runs out.")
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND json_extract(result_json,'$.error.code')='INVALID_ARGUMENT'`, automation.ManageName) != 1 || scalar(t, s, `SELECT count(*) FROM automation`) != 1 {
		t.Fatal("Main created a new zero threshold")
	}
	q.Below = &one
	if _, err = r.ConfigureAutomation(ctx, "new-zero", q, now); err != nil {
		t.Fatal("corrected same draft rejected", err)
	}
	oldDetail, err := s.TaskReader().ScheduledDetail(ctx, old.Ref)
	if err != nil || oldDetail.Below == nil || *oldDetail.Below != 0 {
		t.Fatal("historical below-zero plan reinterpreted", oldDetail, err)
	}
	q.Ref, q.Version, q.Enabled, q.Below = old.Ref, old.Version, false, &zero
	if _, err = r.ConfigureAutomation(ctx, "disable-old-zero", q, now); err != nil {
		t.Fatal("local historical stop required positive threshold", err)
	}
}

func TestScheduleOwnerSelectsFoldAndFiresOnlyAtChosenInstant(t *testing.T) {
	ctx := context.Background()
	for _, choice := range []struct {
		offset string
		hour   int
	}{{"-04:00", 5}, {"-05:00", 6}} {
		t.Run(choice.offset, func(t *testing.T) {
			s, r, _, _ := automationFixture(t)
			installScheduleMethodVersion(t, s, r, "1.0.3", 0)
			now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
			q := automation.OwnerConfiguration{Type: "at", LocalDate: "2026-11-01", LocalClock: "01:30", Timezone: "America/New_York", Instruction: "Review incident report", Enabled: true}
			var diagnostic automation.ScheduleInputError
			if _, err := r.ConfigureAutomation(ctx, "fold", q, now); !errors.As(err, &diagnostic) || diagnostic.Issue != "utc_offset" || strings.Join(diagnostic.UTCOffsets, ",") != "-04:00,-05:00" {
				t.Fatal("missing fold choices", diagnostic, err)
			}
			if scalar(t, s, `SELECT count(*) FROM automation_request`) != 0 {
				t.Fatal("unresolved choice persisted")
			}
			q.UTCOffset = "+01:00"
			if _, err := r.ConfigureAutomation(ctx, "fold", q, now); !errors.As(err, &diagnostic) || diagnostic.Issue != "utc_offset" {
				t.Fatal("foreign offset accepted", err)
			}
			q.UTCOffset = choice.offset
			receipt, err := r.ConfigureAutomation(ctx, "fold", q, now)
			if err != nil {
				t.Fatal(err)
			}
			chosen := time.Date(2026, 11, 1, choice.hour, 30, 0, 0, time.UTC)
			if scalar(t, s, `SELECT count(*) FROM automation WHERE automation_id=? AND trigger_spec=? AND next_fire_at=?`, receipt.Ref, chosen.Format(time.RFC3339), chosen.UnixMicro()) != 1 {
				t.Fatal("wrong instant persisted")
			}
			if err = r.ScheduleAutomations(ctx, chosen.Add(-time.Second)); err != nil || scalar(t, s, `SELECT count(*) FROM automation_fire`) != 0 {
				t.Fatal("early fire", err)
			}
			for _, at := range []time.Time{chosen, chosen.Add(2 * time.Hour)} {
				if err = r.ScheduleAutomations(ctx, at); err != nil {
					t.Fatal(err)
				}
			}
			if scalar(t, s, `SELECT count(*) FROM automation_fire WHERE automation_id=? AND fire_at=?`, receipt.Ref, chosen.UnixMicro()) != 1 || scalar(t, s, `SELECT count(*) FROM automation_fire`) != 1 {
				t.Fatal("incorrect one-time fire count")
			}
		})
	}
}

func TestScheduleOwnerDistinguishesZoneCronAndPastInstant(t *testing.T) {
	ctx := context.Background()
	s, r, _, _ := automationFixture(t)
	installScheduleMethodVersion(t, s, r, "1.0.3", 0)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ kind, spec, zone, field string }{{"cron", "0 8 * * *", "Missing/Zone", "timezone"}, {"cron", "80 * * * *", "UTC", "cron"}, {"at", "2029-12-01T08:00:00Z", "UTC", "future"}} {
		q := automation.OwnerConfiguration{Type: c.kind, Spec: c.spec, Timezone: c.zone, Instruction: "Keep this draft", Enabled: true}
		var d automation.ScheduleInputError
		if _, err := r.ConfigureAutomation(ctx, "draft", q, now); !errors.As(err, &d) || d.Issue != c.field {
			t.Fatalf("expected %s: %v (%+v)", c.field, err, d)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM automation`) != 0 {
		t.Fatal("invalid draft persisted")
	}
}
