package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/task"
	modernsqlite "modernc.org/sqlite"
)

func TestTaskWorkspaceUsesOwnerScopeStablePaginationAndMainDeliveries(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	input := mustSubmit(t, a, "workspace", "Prepare a report")
	mustDrain(t, a)
	reader := s.TaskReader()
	all, err := reader.List(ctx, task.ListQuery{View: "all"})
	if err != nil || len(all.Items) != 1 {
		t.Fatal(all, err)
	}
	ref := all.Items[0].TaskRef
	before := map[string]int{}
	for _, table := range []string{"run", "task", "task_instruction", "task_update", "model_generation", "event_outbox"} {
		before[table] = scalar(t, s, "SELECT count(*) FROM "+table)
	}
	detail, err := reader.Detail(ctx, ref, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.GoalSummary == "" || detail.SourceSequence == 0 {
		t.Fatal("missing original request or goal", detail)
	}
	var source string
	if err = s.db.QueryRow(`SELECT json_extract(message_json,'$.message_id') FROM conversation_intent WHERE sequence=?`, detail.SourceSequence).Scan(&source); err != nil || source != input.MessageID {
		t.Fatal("wrong conversation deep link", source, err)
	}
	if len(detail.Messages) == 0 {
		t.Fatal("Main delivery missing from task detail")
	}
	for _, m := range detail.Messages {
		if m.Role != "assistant" || m.Source != "main" {
			t.Fatal("Worker result became a separate user delivery", m)
		}
	}
	for table, count := range before {
		if scalar(t, s, "SELECT count(*) FROM "+table) != count {
			t.Fatal("browsing mutated", table)
		}
	}
	for i := 0; i < 65; i++ {
		_, err = s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT ?,?,agent_id,agent_id,?,'Workspace goal','[]','active',1,100,100,1 FROM agent_config WHERE singleton=1`, fmt.Sprintf("workspace-%02d", i), fmt.Sprintf("task:workspace-%02d", i), fmt.Sprintf("Workspace %02d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) VALUES('foreign','task:foreign','other-agent','other-agent','Private foreign task','Private goal','[]','active',1,100,100,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Detail(ctx, "task:foreign", ""); !errors.Is(err, task.ErrNotFound) {
		t.Fatal("foreign task visible", err)
	}
	seen := map[string]bool{}
	cursor := ""
	for range 5 {
		page, e := reader.List(ctx, task.ListQuery{View: "all", Query: "Workspace", Cursor: cursor, Limit: 20})
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Items {
			if seen[item.TaskRef] {
				t.Fatal("unstable page duplicated a task", item.TaskRef)
			}
			seen[item.TaskRef] = true
		}
		cursor = page.Next
		if cursor == "" {
			break
		}
		if _, e = reader.List(ctx, task.ListQuery{View: "closed", Query: "Workspace", Cursor: cursor}); !errors.Is(e, task.ErrInvalid) {
			t.Fatal("cursor escaped its view", e)
		}
	}
	if len(seen) != 65 {
		t.Fatal("task history is still capped or leaks another owner", len(seen))
	}
}

func TestTaskWorkspaceDetailKeepsOneSnapshotDuringConcurrentUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const ref = "task:workspace-snapshot"
	var db *sql.DB
	var commitErr error
	var once sync.Once
	updateDuringRead := "test_workspace_update_" + strings.ReplaceAll(newID(), "-", "")
	err := modernsqlite.RegisterScalarFunction(updateDuringRead, 1, func(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		once.Do(func() {
			commitErr = func() error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(ctx, `UPDATE task_data SET task_epoch=2,version=version+1 WHERE task_ref=?`, ref); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at)
SELECT 'concurrent-instruction',task_id,task_epoch,'New instruction','[]',200 FROM task_data WHERE task_ref=?`, ref); err != nil {
					return err
				}
				return tx.Commit()
			}()
		})
		return args[0], commitErr
	})
	if err != nil {
		t.Fatal(err)
	}
	s, _, _, _ := childFixture(t)
	db = s.db
	if _, err = db.ExecContext(ctx, `INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version)
SELECT 'workspace-snapshot',?,agent_id,agent_id,'Old title','Old goal','[]','active',1,100,100,1 FROM agent_config WHERE singleton=1`, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE task RENAME TO task_data;
CREATE VIEW task AS SELECT `+updateDuringRead+`(title) AS title, task_data.* FROM task_data`); err != nil {
		t.Fatal(err)
	}
	read := func(epoch int64, instructions int) task.Detail {
		t.Helper()
		detail, err := s.TaskReader().Detail(ctx, ref, "")
		if err != nil || detail.Epoch != epoch || len(detail.Instructions) != instructions {
			t.Fatalf("mixed workspace snapshot: got epoch %d, instructions %#v, error %v", detail.Epoch, detail.Instructions, err)
		}
		return detail
	}
	read(1, 0)
	detail := read(2, 1)
	if detail.Instructions[0].Epoch != 2 || detail.Instructions[0].Text != "New instruction" {
		t.Fatal(detail.Instructions[0])
	}
}

func TestTaskListPagesEscapedUnicodeSearch(t *testing.T) {
	s := openModels(t)
	ctx := context.Background()
	reader := s.TaskReader()
	query := strings.Repeat("&", 150) + " équipe κόσμος 项目"
	title := strings.Repeat("&", 150) + " ÉQUIPE ΚΌΣΜΟΣ 项目"
	for _, id := range []string{"a", "b"} {
		if _, err := s.db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version) SELECT ?,?,agent_id,agent_id,'Search',?,'[]','active',1,100,100,1 FROM agent_config WHERE singleton=1`, id, "task:"+id, title); err != nil {
			t.Fatal(err)
		}
	}
	q := task.ListQuery{Query: query, View: "all", Limit: 1}
	first, err := reader.List(ctx, q)
	if err != nil || len(first.Items) != 1 || first.Next == "" {
		t.Fatal(first, err)
	}
	q.Cursor = first.Next
	second, err := reader.List(ctx, q)
	if err != nil || len(second.Items) != 1 || second.Items[0].TaskRef == first.Items[0].TaskRef || second.Next != "" {
		t.Fatal(second, err)
	}
	q.Query = "different"
	if _, err = reader.List(ctx, q); !errors.Is(err, task.ErrInvalid) {
		t.Fatal("cursor crossed searches", err)
	}
}
