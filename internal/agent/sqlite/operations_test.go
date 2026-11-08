package sqlite

import (
	"context"
	"database/sql"
	"github.com/z-chenhao/assistant/internal/agent"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperationsPriorSchemaRollbackAndActiveWorkUpgrade(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "state.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	if e = migrate(ctx, db, migrations[:25]); e != nil {
		t.Fatal(e)
	}
	s := &Store{db: db, path: path}
	if e = s.bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	activateLoop(t, s, modelInput())
	h, e := conversationsqlite.Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	registry := tool.FixtureRegistry()
	// Current constructors freeze tool delivery for new Runs. Seed the older
	// fixture with them, then remove that metadata to represent pre-v32 work.
	if _, e = db.Exec(responsiveMainSQL + githubObservationsSQL + taskFeedbackSQL + feishuReplySourcesSQL + githubTaskContinuationSQL + githubRuleObservationSQL); e != nil {
		t.Fatal(e)
	}
	r := s.Runs(h, registry)
	a := agent.Actor{Store: r, Registry: registry, Ready: func(context.Context) bool { return true }}
	receipt := mustSubmit(t, a, "before-upgrade", "PRIVATE_USER_TEXT_29")
	// This pre-v26 fixture never seeded the v51 message-time projection.
	cleanup := strings.Replace(removeGitHubFixtureSchema, "ALTER TABLE conversation_intent DROP COLUMN memory_created_at; ", "", 1)
	if _, e = db.Exec(`DROP TRIGGER main_tool_protocol_retained; DROP TRIGGER main_tool_protocol_immutable; DROP TRIGGER main_tool_protocol_initial; DROP TABLE main_tool_protocol;` + cleanup); e != nil {
		t.Fatal(e)
	}
	var snapshot string
	if e = db.QueryRow(`SELECT model_snapshot_id FROM run WHERE run_id=?`, receipt.RunID).Scan(&snapshot); e != nil {
		t.Fatal(e)
	}
	broken := append(append([]string{}, migrations[:25]...), operationsSQL+"; INVALID SQL")
	if e = migrate(ctx, db, broken); e == nil {
		t.Fatal("broken migration accepted")
	}
	if scalar(t, s, "PRAGMA user_version") != 25 || scalar(t, s, "SELECT count(*) FROM pragma_table_info('event_outbox') WHERE name='dead_at'") != 0 {
		t.Fatal("partial DDL committed")
	}
	h.Close()
	s.Close()
	s, e = Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h, e = conversationsqlite.Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	r = s.Runs(h, registry)
	if scalar(t, s, `SELECT count(*) FROM main_tool_protocol WHERE run_id=?`, receipt.RunID) != 0 {
		t.Fatal("upgrade changed an existing Run's tool delivery protocol")
	}
	if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND model_snapshot_id=? AND status='queued'`, receipt.RunID, snapshot) != 1 {
		t.Fatal("upgrade changed frozen queued work")
	}
	p := &loopProvider{store: s, respond: func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "done"}, nil
	}}
	a = agent.Actor{Store: r, Registry: registry, Provider: p, Ready: func(context.Context) bool { return true }}
	mustDrain(t, a)
	mustDrain(t, a)
	if len(p.requests) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, receipt.RunID) != 1 {
		t.Fatal("active work duplicated or lost")
	}
	d, e := s.Operations(ctx)
	if e != nil || d.Counts["runs_active"] != 0 || d.Counts["outbox_dead"] != 0 {
		t.Fatal(d, e)
	}
	trace, e := s.OperationalTrace(ctx, receipt.RunID)
	if e != nil || len(trace.Points) == 0 {
		t.Fatal("missing correlated trace", trace, e)
	}
	for _, p := range trace.Points {
		if at, e := time.Parse(time.RFC3339Nano, p.At); e != nil || at.Location() != time.UTC {
			t.Fatal("trace timestamp is not RFC3339 UTC", p.At, e)
		}
		if strings.Contains(p.Kind, "PRIVATE") || strings.Contains(p.Tool, "PRIVATE") {
			t.Fatal("trace leaked payload")
		}
	}
	// A diagnostic is read-only; it never wakes a model or notifies the user.
	before := scalar(t, s, "SELECT count(*) FROM event_outbox")
	if _, e = s.Operations(ctx); e != nil || scalar(t, s, "SELECT count(*) FROM event_outbox") != before {
		t.Fatal("diagnostics mutated domain")
	}
}
