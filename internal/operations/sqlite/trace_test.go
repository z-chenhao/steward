package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	modernsqlite "modernc.org/sqlite"
)

func TestOperationalTraceUsesOneSnapshotAndReportsReadFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var db *sql.DB
	var writeErr error
	var once sync.Once
	hook := fmt.Sprintf("test_change_trace_%d", time.Now().UnixNano())
	err := modernsqlite.RegisterScalarFunction(hook, 1, func(_ *modernsqlite.FunctionContext, values []driver.Value) (driver.Value, error) {
		once.Do(func() {
			_, writeErr = db.ExecContext(ctx, `BEGIN IMMEDIATE; UPDATE data SET status='completed'; INSERT INTO run_trace VALUES('run-a',1,'{"kind":"agent_end"}',100); COMMIT`)
		})
		return values[0], writeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=WAL; CREATE TABLE data(run_id,status,parent_run_id,root_run_id);
 CREATE TABLE run_trace(run_id,sequence,event_json,occurred_at);
 INSERT INTO data VALUES('run-a','running',NULL,NULL);
 CREATE VIEW run AS SELECT run_id,`+hook+`(status) AS status,parent_run_id,root_run_id FROM data`)
	if err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db}
	first, err := store.Trace(ctx, "run-a")
	if err != nil || first.Status != "running" || len(first.Points) != 0 {
		t.Fatal(first, err)
	}
	second, err := store.Trace(ctx, "run-a")
	if err != nil || second.Status != "completed" || len(second.Points) != 1 {
		t.Fatal(second, err)
	}
	if _, err = store.Trace(ctx, "missing"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err = store.Trace(cancelled, "run-a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	db.Close()
	if _, err = store.Trace(ctx, "run-a"); !errors.Is(err, agent.ErrStorage) {
		t.Fatal(err)
	}
}
