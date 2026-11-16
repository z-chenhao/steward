package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

// Restrict scheduling to one original Run while retaining the production
// Actor's heartbeat/registry/claim pipeline. Descendants remain durably queued.
type discoveryOneRunStore struct {
	*Runs
	run     string
	offered bool
}

func (s *discoveryOneRunStore) Next(context.Context) (string, error) {
	if s.offered {
		return "", agent.ErrNotFound
	}
	s.offered = true
	return s.run, nil
}
func driveDiscoveryRun(t *testing.T, a agent.Actor, r *Runs, id string) {
	t.Helper()
	a.Store = &discoveryOneRunStore{Runs: r, run: id}
	if err := a.Drain(context.Background()); err != nil {
		var detail string
		_ = r.host.db.QueryRow(`SELECT json_object('status',x.status,'wait',x.wait_reason,'desired',x.desired_state,'claim_epoch',c.epoch,'lease_delta_us',c.expires_at-?,'claim_token_present',c.token IS NOT NULL,'children',(SELECT count(*) FROM run WHERE parent_run_id=x.run_id),'invocations',(SELECT group_concat(tool_name||':'||status) FROM tool_invocation WHERE run_id=x.run_id)) FROM run x JOIN run_claim c ON c.run_id=x.run_id WHERE x.run_id=?`, time.Now().UnixMicro(), id).Scan(&detail)
		t.Fatalf("actual Actor for Run %s: %v; %s", id, err, detail)
	}
}

// Selection changes precede actual child admission. Positive descendant counts,
// real JavaScript and both accepted replies prove inheritance beyond the Root.
func TestToolDiscoveryActualChildrenAndGrandchildrenKeepRootVersion(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t, largeToolCatalog()...)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	r.ConfigureJavaScript(realCodeRuntime(t))
	code := func(id string) llm.GenerationResult {
		raw, _ := json.Marshal(map[string]string{"source": `const found=await searchTools('catalog',{limit:1});return found[0]?.name??'EMPTY';`})
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: id, Name: codemode.ToolName, Arguments: string(raw)}}}
	}
	spawn := func(id, instruction string) llm.GenerationResult {
		raw, _ := json.Marshal(map[string]string{"task": instruction})
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: id, Name: agent.RunToolName, Arguments: string(raw)}}}
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main tree result observed"}, nil
		}
		if !isRoot(req) {
			if last.Role == "tool" {
				return llm.GenerationResult{Text: "Task accepted"}, nil
			}
			return calls(task.ManageName, `{"operation":"create","task":"Root-private instruction: discover authorized metadata in an explicit child tree."}`), nil
		}
		if last.Role == "tool" {
			if last.ToolCallID == "spawn-leaf" {
				return code("branch-search"), nil
			}
			return llm.GenerationResult{Text: "Worker result observed"}, nil
		}
		switch last.Text {
		case "discovery branch":
			return spawn("spawn-leaf", "discovery leaf"), nil
		case "discovery leaf":
			return code("leaf-search"), nil
		default:
			return spawn("spawn-branch", "discovery branch"), nil
		}
	}
	queue := func(key string) string {
		t.Helper()
		receipt := mustSubmit(t, a, key, "Delegate metadata discovery")
		driveDiscoveryRun(t, a, r, receipt.RunID)
		if n := scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root' AND status='queued'`); n != 1 {
			t.Fatal("expected one queued Root", n)
		}
		var id string
		if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued'`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	drive := func(root, target string, managed, disabled bool) {
		t.Helper()
		driveDiscoveryRun(t, a, r, root)
		// Root is now waiting on the actual newly admitted Child; no fixture select.
		if n := scalar(t, s, `SELECT count(*) FROM run WHERE parent_run_id=? AND worker_position='child' AND status='queued'`, root); n != 1 {
			t.Fatal("Root did not admit exactly one real Child", n)
		}
		mustDrain(t, a)
		var graph sql.NullString
		var services string
		if err := s.db.QueryRow(`SELECT graph_revision,managed_services_json FROM rsi_run WHERE run_id=?`, root).Scan(&graph, &services); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(services, contract.Contract) != managed {
			t.Fatal("Root migration boundary changed", root, services)
		}
		rows, err := s.db.Query(`SELECT c.run_id,c.depth,c.parent_run_id,c.status,b.graph_revision,b.managed_services_json FROM run c JOIN rsi_run b ON b.run_id=c.run_id WHERE c.root_run_id=? AND c.worker_position='child' ORDER BY c.depth`, root)
		if err != nil {
			t.Fatal(err)
		}
		type child struct {
			id, parent, status, services string
			depth                        int
			graph                        sql.NullString
		}
		var children []child
		for rows.Next() {
			var c child
			if err := rows.Scan(&c.id, &c.depth, &c.parent, &c.status, &c.graph, &c.services); err != nil {
				t.Fatal(err)
			}
			children = append(children, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(children) != 2 || children[0].depth != 1 || children[1].depth != 2 || children[0].parent != root || children[1].parent != children[0].id {
			t.Fatal("missing actual Child/Grandchild ancestry", children)
		}
		for _, c := range children {
			if c.status != "completed" || c.graph != graph || c.services != services {
				t.Fatal("descendant lost Root graph/services", c, graph, services)
			}
			if n := scalar(t, s, `SELECT count(*) FROM run c JOIN run p ON p.run_id=c.parent_run_id JOIN capability_snapshot cc ON cc.snapshot_id=c.capability_snapshot_id JOIN capability_snapshot pc ON pc.snapshot_id=p.capability_snapshot_id WHERE c.run_id=? AND c.model_snapshot_id=p.model_snapshot_id AND c.task_id=p.task_id AND c.task_epoch=p.task_epoch AND cc.tools_json=pc.tools_json AND cc.data_scope=pc.data_scope`, c.id); n != 1 {
				t.Fatal("descendant changed model/capability/scope", c.id, n)
			}
			var result string
			if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=? AND attempt_count=1`, c.id, codemode.ToolName).Scan(&result); err != nil {
				t.Fatal("actual child script absent or replayed", c.id, err)
			}
			count := scalar(t, s, `SELECT count(*) FROM code_search_reply q JOIN code_manifest m USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?`, c.id)
			if managed && count != 1 || !managed && count != 0 {
				t.Fatal("descendant bypassed managed accepted discovery", c.id, count, managed)
			}
			if disabled {
				if n := scalar(t, s, `SELECT count(*) FROM code_search_reply q JOIN code_manifest m USING(execution_id) JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=? AND json_extract(q.reply_json,'$.error.code')='PROVIDER_UNAVAILABLE'`, c.id); n != 1 {
					t.Fatal("disabled child restored native ranking", c.id, n, result)
				}
			} else if !strings.Contains(result, target) {
				t.Fatal("actual child used different ranking version", c.id, result, target)
			}
		}
	}
	legacy := queue("discovery-native-tree")
	h := installToolDiscoveryPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drive(legacy, "mcp__catalog__lookup_000", false, false)
	old := queue("discovery-old-tree")
	second := selectToolDiscoveryVersion(t, h, first.Revision, "1.1.0")
	drive(old, "mcp__catalog__lookup_000", true, false)
	current := queue("discovery-new-tree")
	drive(current, "mcp__catalog__lookup_499", true, false)
	retained := queue("discovery-retired-tree")
	activateDelegation(t, h, "discovery-tree-rollback", second, first.Revision, "discovery-owner-fixture")
	g, err := h.Store.Graph(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Retire(ctx, g.Graph.Bindings[0].PackageDigest); err != nil {
		t.Fatal(err)
	}
	drive(retained, "mcp__catalog__lookup_499", true, false)
	rolled := queue("discovery-rolled-tree")
	if _, err := h.Store.Deactivate(ctx, "discovery-tree-deactivate", first.Revision); err != nil {
		t.Fatal(err)
	}
	drive(rolled, "mcp__catalog__lookup_000", true, false)
	disabled := queue("discovery-disabled-tree")
	drive(disabled, "", true, true)
	if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 12 || scalar(t, s, `SELECT count(*) FROM agent_invocation`) != 12 || scalar(t, s, `SELECT count(*) FROM task`) != 6 {
		t.Fatal("tree count or independent Task boundary changed")
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name LIKE 'mcp__catalog__%'`) != 0 {
		t.Fatal("metadata discovery executed a candidate")
	}
}

// Forward67 replaces only the guard. Old frozen records and accepted child
// identity remain byte-stable; opening the old schema does not opt them in.
func TestToolDiscoveryV66ChildAdmissionMigrationKeepsFrozenTree(t *testing.T) {
	ctx := context.Background()
	s, r, a, p := childFixture(t)
	claim, run, gen, calls := childAdmission(t, s, r, a, p, `{"task":"retained child"}`)
	if _, err := r.ToolStore(claim).Accept(ctx, gen.Request.GenerationID, calls, run.Tools); err != nil {
		t.Fatal(err)
	}
	old := layeredMemorySQL[strings.Index(layeredMemorySQL, "DROP TRIGGER IF EXISTS child_initial;"):]
	if _, err := s.db.Exec(removeGitHubSourceMethodFixtureSchema + old + `;PRAGMA user_version=66`); err != nil {
		t.Fatal(err)
	}
	snapshot := func(db *sql.DB) map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, table := range []string{"run", "capability_snapshot", "run_context_policy", "rsi_run", "model_generation", "tool_invocation", "agent_invocation", "run_tree", "run_claim"} {
			query := `SELECT * FROM ` + table + ` ORDER BY 1,2`
			if table == "model_generation" {
				query = `SELECT ` + historicalGenerationColumnsV80 + ` FROM model_generation ORDER BY 1,2`
			}
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			records := [][]any{}
			for rows.Next() {
				record := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for i := range record {
					pointers[i] = &record[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				records = append(records, record)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			result[table] = string(raw)
		}
		return result
	}
	before := snapshot(s.db)
	data := filepath.Dir(s.path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	after := snapshot(next.db)
	if scalar(t, next, `SELECT count(*) FROM model_generation WHERE reference_rates_managed!=0`) != 0 || scalar(t, next, `SELECT count(*) FROM generation_reference_rates`) != 0 {
		t.Fatal("migration opted historical generations into new pricing")
	}
	for table, want := range before {
		if after[table] != want {
			t.Fatal("forward migration rewrote retained facts", table)
		}
	}
	var guard string
	if err := next.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='child_initial' AND type='trigger'`).Scan(&guard); err != nil || !strings.Contains(guard, "child EXCEPT SELECT parent.value") {
		t.Fatal("new membership guard not installed", err)
	}
	if scalar(t, next, `PRAGMA user_version`) != SchemaVersion() || scalar(t, next, `SELECT count(*) FROM run WHERE worker_position='child'`) != 1 || scalar(t, next, `SELECT count(*) FROM code_search_reply`) != 0 {
		t.Fatal("forward schema or retained Child changed")
	}
}
