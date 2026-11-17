package sqlite

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/interaction/web"
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
