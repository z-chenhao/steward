package sqlite

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleScanExcludesRevokedPageBeforeLimiting(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	db, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r.ConfigureMemory(db, memory.Vault{})
	installScheduleOwnerMethod(t, s, r)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	q := automation.OwnerConfiguration{Instruction: "weekly report", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}
	first, err := r.ConfigureAutomation(ctx, "first", q, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec("WITH RECURSIVE nums(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM nums WHERE n<99) INSERT INTO automation(automation_id,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version,method_graph) SELECT 'copy-'||n,agent_id,instruction,trigger_type,trigger_spec,timezone,enabled,next_fire_at,created_at,updated_at,version,method_graph FROM automation,nums WHERE automation_id=?", first.Ref)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ManageMemory(ctx, "forget", memory.Mutation{Operation: "forget", Category: "health"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := r.ConfigureAutomation(ctx, "valid", q, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if e := r.ScheduleAutomations(ctx, now.Add(2*time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	if scalar(t, s, "select count(*) from automation_fire where automation_id=?", current.Ref) != 1 || scalar(t, s, "select count(*) from automation_fire") != 1 {
		t.Fatal("expected only the fresh timer to fire once")
	}
}

func TestScheduleScanIsolatesMissingPackageAndRestoresOriginalFire(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	old := createAutomation(t, r, now, nil)
	h := installScheduleOwnerMethod(t, s, r)
	q := automation.OwnerConfiguration{Instruction: "with missing calendar package", Type: "cron", Spec: "* * * * *", Timezone: "UTC", Enabled: true}
	current, err := r.ConfigureAutomation(ctx, "current", q, now)
	if err != nil {
		t.Fatal(err)
	}
	due := now.AddDate(0, 0, 2)
	r.SetTestAutomationClock(due)
	var originalGraph string
	var originalFire int64
	if err = s.db.QueryRow("select method_graph,next_fire_at from automation where automation_id=?", current.Ref).Scan(&originalGraph, &originalFire); err != nil {
		t.Fatal(err)
	}
	graph, err := h.Store.Graph(ctx, originalGraph)
	if err != nil {
		t.Fatal(err)
	}
	pkg := h.Catalog.Path(graph.Graph.Bindings[0].PackageDigest)
	installScheduleMethodVersion(t, s, r, "1.1.0", 3)
	if err = os.Rename(pkg, pkg+".off"); err != nil {
		t.Fatal(err)
	}
	err = r.ScheduleAutomations(ctx, due)
	if !errors.Is(err, rsi.ErrNotFound) || !strings.Contains(err.Error(), current.Ref) {
		t.Fatal("missing per-schedule diagnostic", err)
	}
	if scalar(t, s, "select count(*) from automation_fire where automation_id=?", old) != 1 {
		t.Fatal("missing package blocked independent schedule")
	}
	if id, err := r.Next(ctx); err != nil || id == "" {
		t.Fatal("missing package blocked Run selection", err)
	}
	if scalar(t, s, "select count(*) from automation_fire where automation_id=?", current.Ref) != 0 {
		t.Fatal("missing package advanced the frozen schedule")
	}
	if err = os.Rename(pkg+".off", pkg); err != nil {
		t.Fatal(err)
	}
	if err = r.ScheduleAutomations(ctx, due); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, "select count(*) from automation_fire f join event_outbox e using(event_id) where f.automation_id=? and f.method_graph=? and f.fire_at=?", current.Ref, originalGraph, originalFire) != 1 {
		t.Fatal("restore did not release original fire and outbox")
	}
}
