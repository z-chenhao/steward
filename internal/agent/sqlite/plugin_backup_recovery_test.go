package sqlite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/automation"
	convsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/maintenance"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestBackupResumesRunPinnedToHistoricalCustomCalendar(t *testing.T) {
	ctx := context.Background()
	s, r, actor, provider := automationFixture(t)
	root := s.DataRoot()
	mem, err := memorysqlite.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = mem.Close(); err != nil {
		t.Fatal(err)
	}
	host := installScheduleOwnerMethod(t, s, r)
	selected, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := mustSubmit(t, actor, "before-cold-backup", "Schedule custom calendar")
	if _, err = host.Store.Deactivate(ctx, "deactivate-after-freeze", selected.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	archive := filepath.Join(parent, "archive")
	dest := filepath.Join(parent, "restore")
	key := bytes.Repeat([]byte{42}, 32)
	versions := map[string]int{"state.db": SchemaVersion(), "memory.db": memorysqlite.SchemaVersion, "conversation.db": convsqlite.SchemaVersion}
	if _, err = maintenance.Backup(ctx, root, archive, key, versions); err != nil {
		t.Fatal(err)
	}
	if _, err = maintenance.Restore(ctx, archive, dest, key, versions); err != nil {
		t.Fatal(err)
	}
	catalog := rsi.NewCatalog(dest)
	catalog.Launcher = host.Catalog.Launcher
	if err = os.Rename(catalog.Root, catalog.Root+".pending"); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Activate(dest, "source-retired-and-outcomes-reconciled"); err == nil || !maintenance.Pending(dest) {
		t.Fatal("activated incomplete package inventory")
	}
	if err = os.Rename(catalog.Root+".pending", catalog.Root); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Activate(dest, "source-retired-and-outcomes-reconciled"); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	history, err := convsqlite.Open(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	runs := restored.Runs(history, r.registry)
	runs.ConfigurePlugins(catalog)
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	runs.SetTestAutomationClock(now)
	actor.Store = runs
	provider.store = restored
	provider.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "configured"}, nil
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "schedule", Name: automation.ManageName, Arguments: `{"operation":"create","instruction":"restored intent","triggerType":"cron","triggerSpec":"plugin-only","timezone":"UTC"}`}}}, nil
	}
	mustDrain(t, actor)
	if scalar(t, restored, "select count(*) from run where run_id=? and status='completed'", original.RunID) != 1 || scalar(t, restored, "select count(*) from automation where method_graph=? and next_fire_at=?", selected.Revision, now.Add(3*time.Minute).UnixMicro()) != 1 {
		t.Fatal("restored Run did not execute its original calendar method")
	}
}
