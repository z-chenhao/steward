package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Existing populated-version fixtures reconstruct their old schema before
// reopening. Remove these new, empty indexes along with the later tables.
const preGitHubTaskColumns = `task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,progress_summary,created_at,updated_at,completed_at,cancelled_at,version,completion_policy`
const removeGitHubContinuationFixtureSchema = removeUIInteractionFixtureSchema + `DROP TABLE IF EXISTS ui_widget; DROP TABLE IF EXISTS feishu_task_question; DROP TABLE IF EXISTS github_rule_decision; ALTER TABLE github_observation DROP COLUMN binding_generation; DROP TABLE IF EXISTS github_task_followup; DROP TABLE IF EXISTS github_task_binding; ALTER TABLE task DROP COLUMN explicit_end_epoch;
 DROP TRIGGER run_desired_state; CREATE TRIGGER run_desired_state BEFORE UPDATE ON run WHEN NEW.desired_state NOT IN('running','task_paused','task_cancelled','memory_forgotten') OR (OLD.desired_state!='running' AND NEW.desired_state='running') BEGIN SELECT RAISE(ABORT,'invalid desired state'); END; `
const removeGitHubFixtureSchema = removeGitHubContinuationFixtureSchema + `DROP TRIGGER message_reply_source; ALTER TABLE message_route DROP COLUMN reply_source_id; DROP TABLE IF EXISTS task_feedback; DROP TABLE IF EXISTS github_observation; DROP TABLE IF EXISTS github_watch; `
const removeInspectionFixtureSchema = `DROP TABLE IF EXISTS task_code_environment; ` + removeGitHubFixtureSchema + `DROP TABLE IF EXISTS dispatch_observation; DROP INDEX IF EXISTS run_observation_root; DROP INDEX run_inspection_created; DROP INDEX run_inspection_status; DROP INDEX run_inspection_task; DROP INDEX task_request_reference; `

func TestInspectionUsesRealMainTaskRootChildCausality(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	main := mustSubmit(t, a, "inspect-tree", "PRIVATE_CONVERSATION_NOT_METADATA")
	mustDrain(t, a)
	inspector := s.Inspector()
	before := map[string]int{}
	for _, table := range []string{"run", "task_request", "task_invocation", "tool_invocation", "model_generation", "run_trace", "event_outbox"} {
		before[table] = countRows(t, s, table)
	}
	relations, err := inspector.Relationships(ctx, observability.RelationshipQuery{RunID: main.RunID})
	if err != nil || relations.Selected == nil || relations.Selected.Role != "main" || len(relations.Tasks) != 1 || relations.Task != nil || len(relations.Executions) != 0 {
		t.Fatal("Main relationship projection", relations, err)
	}
	link := relations.Tasks[0]
	if link.Operation != "create" || link.Invocation == "" || link.Ref == "" {
		t.Fatal(link)
	}
	tree, err := inspector.Relationships(ctx, observability.RelationshipQuery{TaskRef: link.Ref})
	if err != nil || tree.Creator == nil || tree.Creator.ID != main.RunID || tree.Task == nil || len(tree.Executions) != 3 {
		t.Fatal("Task source/tree", tree, err)
	}
	var root string
	parents := map[string]string{}
	for _, run := range tree.Executions {
		parents[run.ID] = run.Parent
		if run.Role == "root" {
			root = run.ID
			if run.Parent != "" || run.Root != run.ID || run.Depth != 0 {
				t.Fatal("Root was turned into Main's Child", run)
			}
		} else if run.Role != "child" || run.Parent == "" {
			t.Fatal("Child relationship lost", run)
		}
		selected, e := inspector.Relationships(ctx, observability.RelationshipQuery{RunID: run.ID, Limit: 1})
		if e != nil || selected.Selected == nil || selected.Selected.ID != run.ID || selected.Selected.Parent != run.Parent || selected.Task.Ref != link.Ref {
			t.Fatal("selecting Worker lost Task/parent", selected, e)
		}
	}
	for _, run := range tree.Executions {
		if run.Root != root || run.Depth > 0 && parents[run.Parent] == main.RunID {
			t.Fatal("incorrect ancestry", run)
		}
	}
	// Relationship pagination remains complete even when parents occur on a
	// previous page. The selected execution's own links are always returned.
	var paged []observability.Execution
	cursor := ""
	for range 4 {
		page, e := inspector.Relationships(ctx, observability.RelationshipQuery{TaskRef: link.Ref, Limit: 1, Cursor: cursor})
		if e != nil {
			t.Fatal(e)
		}
		paged = append(paged, page.Executions...)
		cursor = page.Next
		if cursor == "" {
			break
		}
	}
	if !reflect.DeepEqual(paged, tree.Executions) {
		t.Fatal("tree paging dropped/duplicated nodes")
	}
	raw, _ := json.Marshal(tree)
	for _, private := range []string{"PRIVATE_CONVERSATION_NOT_METADATA", "anchor_task", "claim_token", "checkpoint_json", "model_snapshot", "arguments_json", "endpoint"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private execution payload exposed", private)
		}
	}
	for table, n := range before {
		if got := countRows(t, s, table); got != n {
			t.Fatal("inspection wrote domain data", table, n, got)
		}
	}
	// A deferred read snapshot must not wait for an unrelated writer's
	// reserved lock on the state pool configured for immediate transactions.
	writer, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	read, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err = inspector.Relationships(read, observability.RelationshipQuery{TaskRef: link.Ref}); err != nil {
		t.Fatal("observer contended for writer lock", err)
	}
}

func TestInspectionKeysetFiltersScopeAndInvalidInputs(t *testing.T) {
	ctx := context.Background()
	s, _, _, a, _ := loopFixture(t)
	first := mustSubmit(t, a, "inspection-list", "PRIVATE_USER_TEXT")
	mustDrain(t, a)
	// Equal timestamps exercise the ID tiebreaker independently of UUID time.
	for _, id := range []string{"page-a", "page-b", "page-c", "foreign-run"} {
		owner := idAgent(t, s)
		if id == "foreign-run" {
			owner = "another-agent"
		}
		_, err := s.db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,depth,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) SELECT ?,?,conversation_id,'main','queued',0,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,created_at,1 FROM run WHERE run_id=?`, id, owner, first.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`UPDATE run SET status='cancelled',ended_at=created_at,version=version+1 WHERE run_id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	inspector := s.Inspector()
	all, err := inspector.Executions(ctx, observability.ExecutionQuery{})
	if err != nil || len(all.Items) != 4 {
		t.Fatal(all, err)
	}
	var paged []observability.Execution
	cursor := ""
	for range 6 {
		page, e := inspector.Executions(ctx, observability.ExecutionQuery{Limit: 1, Cursor: cursor})
		if e != nil {
			t.Fatal(e)
		}
		paged = append(paged, page.Items...)
		cursor = page.Next
		if cursor == "" {
			break
		}
	}
	if !reflect.DeepEqual(paged, all.Items) {
		t.Fatal("list keyset lost or repeated equal-time rows")
	}
	filtered, err := inspector.Executions(ctx, observability.ExecutionQuery{Role: "main", Status: "cancelled"})
	if err != nil || len(filtered.Items) != 3 {
		t.Fatal(filtered, err)
	}
	at, err := time.Parse(time.RFC3339Nano, all.Items[0].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []observability.ExecutionQuery{{Since: at.Add(time.Second)}, {Until: at}} {
		page, err := inspector.Executions(ctx, query)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("wrong time bound", page, err)
		}
	}
	for _, id := range []string{"foreign-run", "absent"} {
		if _, err = inspector.Relationships(ctx, observability.RelationshipQuery{RunID: id}); !errors.Is(err, observability.ErrNotFound) {
			t.Fatal("foreign/missing execution revealed", err)
		}
	}
	for _, query := range []observability.ExecutionQuery{{Limit: 101}, {Role: "worker"}, {Status: "paused"}, {Cursor: "invalid"}, {Since: at, Until: at}} {
		if _, err = inspector.Executions(ctx, query); !errors.Is(err, observability.ErrInvalid) {
			t.Fatal("invalid list query accepted", query, err)
		}
	}
	for _, query := range []observability.RelationshipQuery{{}, {RunID: first.RunID, TaskRef: "task:other"}, {RunID: first.RunID, Cursor: "invalid"}, {TaskRef: "task:absent"}} {
		_, err = inspector.Relationships(ctx, query)
		if err == nil {
			t.Fatal("invalid/missing relationship query accepted", query)
		}
	}
}

func TestInspectionIndexUpgradeRollbackAndFrozenData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(ctx, db, migrations[:29]); err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db, path: path}
	if err = s.bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	activateLoop(t, s, modelInput())
	history, err := conversationsqlite.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	registry := tool.FixtureRegistry()
	// Seed with current constructors, then remove the v32 delivery metadata
	// so the queued Run represents an actual pre-protocol migration fixture.
	if _, err = db.Exec(responsiveMainSQL + githubObservationsSQL + taskFeedbackSQL + feishuReplySourcesSQL + githubTaskContinuationSQL + githubRuleObservationSQL); err != nil {
		t.Fatal(err)
	}
	actor := agent.Actor{Store: s.Runs(history, registry), Registry: registry, Ready: func(context.Context) bool { return true }}
	receipt := mustSubmit(t, actor, "before-inspection-upgrade", "frozen before index upgrade")
	// This pre-v30 fixture never seeded the v51 message-time projection.
	cleanup := strings.Replace(removeGitHubFixtureSchema, "ALTER TABLE conversation_intent DROP COLUMN memory_created_at; ", "", 1)
	if _, err = db.Exec(`DROP TRIGGER main_tool_protocol_retained; DROP TRIGGER main_tool_protocol_immutable; DROP TRIGGER main_tool_protocol_initial; DROP TABLE main_tool_protocol;` + cleanup); err != nil {
		t.Fatal(err)
	}
	var checkpoint string
	if err = db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, receipt.RunID).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	var frozen string
	if err = db.QueryRow(`SELECT group_concat(snapshot_id||generation_defaults_json||compatibility_json||capabilities_json,'') FROM model_snapshot`).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	broken := append(append([]string{}, migrations[:29]...), inspectionSQL+";INVALID SQL")
	if err = migrate(ctx, db, broken); err == nil {
		t.Fatal("partial index migration accepted")
	}
	if scalar(t, s, "PRAGMA user_version") != 29 || scalar(t, s, `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'run_inspection_%'`) != 0 {
		t.Fatal("index migration did not roll back")
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = db.QueryRow(`SELECT group_concat(snapshot_id||generation_defaults_json||compatibility_json||capabilities_json,'') FROM model_snapshot`).Scan(&after); err != nil || after != frozen {
		t.Fatal("read indexes rewrote frozen snapshots", err)
	}
	var kept string
	if err = db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, receipt.RunID).Scan(&kept); err != nil || kept != checkpoint {
		t.Fatal("queued Run changed", err)
	}
	if scalar(t, s, "PRAGMA user_version") != SchemaVersion() || scalar(t, s, `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'run_inspection_%'`) != 3 {
		t.Fatal("inspection indexes missing")
	}
}
