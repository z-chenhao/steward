package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/observability"
)

func contextFixture(t *testing.T, old, next any) (*Inspector, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE agent_config(singleton INTEGER,agent_id TEXT);INSERT INTO agent_config VALUES(1,'owner');CREATE TABLE run(run_id TEXT,agent_id TEXT);INSERT INTO run VALUES('run','owner'),('foreign','other');CREATE TABLE model_generation(generation_id TEXT,run_id TEXT,step_index INTEGER,request_json TEXT,request_hash TEXT,status TEXT,error_code TEXT,created_at INTEGER,result_json TEXT);CREATE TABLE context_compaction(run_id TEXT,step_index INTEGER,provenance_json TEXT);CREATE TABLE run_recall(run_id TEXT,step_index INTEGER,hits_json TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	for step, request := range []any{old, next} {
		raw, e := json.Marshal(request)
		if e != nil {
			t.Fatal(e)
		}
		_, e = db.Exec(`INSERT INTO model_generation VALUES(?,'run',?,?,'frozen-hash','completed','',100,'{"text":"response","tool_calls":[{"id":"call","name":"echo","arguments":"{}"}]}')`, step, step, string(raw))
		if e != nil {
			t.Fatal(e)
		}
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	return reader, db
}

func TestFrozenContextCompleteUTF8NULBoundedAndScoped(t *testing.T) {
	text := strings.Repeat("汉字🦊", 2400) + "\x00COMPLETE_END"
	messages := []any{}
	for range 47 {
		messages = append(messages, map[string]any{"role": "user", "text": text, "source": map[string]any{"kind": "conversation", "ref": "conversation:known"}})
	}
	request := map[string]any{"messages": messages, "model_snapshot": map[string]any{"credential_ref": "PRIVATE_CONFIG"}}
	s, db := contextFixture(t, request, request)
	ctx := context.Background()
	q := observability.ContextQuery{RunID: "run", Step: 1, Section: "messages"}
	page, err := s.Context(ctx, q)
	if err != nil || len(page.Items) != 40 || page.Next == nil || *page.Next != 40 {
		t.Fatal(page, err)
	}
	if len(page.Content) > observability.ContextChunk || page.Selected.SourceRef != "conversation:known" {
		t.Fatal("unbounded or missing source")
	}
	pageJSON, _ := json.Marshal(page)
	if strings.Contains(string(pageJSON), "PRIVATE_CONFIG") {
		t.Fatal("snapshot leaked")
	}
	var complete strings.Builder
	for {
		part, e := s.Context(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		complete.WriteString(part.Content)
		if part.NextOffset == nil {
			break
		}
		q.Offset = *part.NextOffset
	}
	if complete.String() != text {
		t.Fatal("UTF-8 or embedded NUL silently truncated")
	}
	q.Offset = 1
	if _, e := s.Context(ctx, q); !errors.Is(e, observability.ErrInvalid) {
		t.Fatal("invalid rune boundary", e)
	}
	q.Offset = 0
	q.After = 40
	q.Item = 40
	page, err = s.Context(ctx, q)
	if err != nil || len(page.Items) != 7 || page.Next != nil {
		t.Fatal(page, err)
	}
	for _, run := range []string{"missing", "foreign"} {
		if _, e := s.Rounds(ctx, observability.RoundQuery{RunID: run, After: -1}); !errors.Is(e, observability.ErrNotFound) {
			t.Fatal(e)
		}
		q.RunID = run
		if _, e := s.Context(ctx, q); !errors.Is(e, observability.ErrNotFound) {
			t.Fatal(e)
		}
		if _, e := s.ContextDiff(ctx, observability.DiffQuery{RunID: run, Step: 1, Section: "messages"}); !errors.Is(e, observability.ErrNotFound) {
			t.Fatal("foreign/missing diff revealed", e)
		}
	}
	// Observation remains available while the writer holds its transaction.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE run SET agent_id=agent_id`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Context(ctx, observability.ContextQuery{RunID: "run", Step: 1, Section: "messages"}); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenRoundPaginationRetainsSparseSteps(t *testing.T) {
	s, db := contextFixture(t, map[string]any{"messages": []any{}}, map[string]any{"messages": []any{}})
	for step := int64(4); step < 170; step += 2 {
		if _, err := db.Exec(`INSERT INTO model_generation VALUES(?,'run',?,'{"messages":[]}', 'frozen-hash','pending','',100,NULL)`, step, step); err != nil {
			t.Fatal(err)
		}
	}
	after := int64(-1)
	steps := []int64{}
	for {
		page, err := s.Rounds(context.Background(), observability.RoundQuery{RunID: "run", After: after})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 40 {
			t.Fatal("unbounded rounds")
		}
		for _, r := range page.Items {
			if r.Step <= after {
				t.Fatal("duplicate/out of order step")
			}
			steps = append(steps, r.Step)
			after = r.Step
		}
		if page.Next == nil {
			break
		}
		if *page.Next != after {
			t.Fatal("round cursor skipped last step")
		}
	}
	if len(steps) != 85 || steps[0] != 0 || steps[1] != 1 || steps[2] != 4 || steps[len(steps)-1] != 168 {
		t.Fatal(steps)
	}
	view, err := s.Context(context.Background(), observability.ContextQuery{RunID: "run", Step: 4, Section: "messages"})
	if err != nil || view.Previous == nil || *view.Previous != 1 {
		t.Fatal("previous is saved step, not step-1", view, err)
	}
}

func TestFrozenContextDiffUsesStoredReferencesAndPages(t *testing.T) {
	m := func(ref, text string) map[string]any {
		return map[string]any{"role": "user", "text": text, "source": map[string]string{"ref": ref}}
	}
	old := map[string]any{"messages": []any{m("kept", "same"), m("removed", "gone"), m("changed", "before"), m("moved", "same")}, "tools": []any{map[string]string{"name": "echo", "input_schema": "{}"}}}
	next := map[string]any{"messages": []any{m("moved", "same"), m("kept", "same"), m("changed", "after"), m("added", "new")}, "tools": []any{map[string]string{"name": "echo", "input_schema": "changed"}}}
	s, db := contextFixture(t, old, next)
	ctx := context.Background()
	diff, err := s.ContextDiff(ctx, observability.DiffQuery{RunID: "run", Step: 1, Section: "messages"})
	if err != nil || diff.Previous == nil || *diff.Previous != 0 || len(diff.Items) != 5 {
		t.Fatal(diff, err)
	}
	counts := map[string]int{}
	for _, v := range diff.Items {
		counts[v.Kind]++
	}
	if counts["added"] != 1 || counts["removed"] != 1 || counts["changed"] != 1 || counts["moved"] != 2 {
		t.Fatal(counts)
	}
	tools, err := s.ContextDiff(ctx, observability.DiffQuery{RunID: "run", Step: 1, Section: "tools"})
	if err != nil || len(tools.Items) != 1 || tools.Items[0].Kind != "changed" {
		t.Fatal(tools, err)
	}
	first, err := s.ContextDiff(ctx, observability.DiffQuery{RunID: "run", Step: 0, Section: "messages"})
	if err != nil || first.Previous != nil || len(first.Items) != 0 {
		t.Fatal(first, err)
	}
	// Duplicate refs are matched by occurrence, never multiplied by a join.
	items := []any{}
	for i := range 83 {
		items = append(items, m("duplicate", strings.Repeat("a", i)))
	}
	raw, _ := json.Marshal(map[string]any{"messages": items})
	if _, err = db.Exec(`UPDATE model_generation SET request_json=? WHERE step_index=1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	seen := 0
	after := 0
	for {
		page, e := s.ContextDiff(ctx, observability.DiffQuery{RunID: "run", Step: 1, Section: "messages", After: after})
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 40 {
			t.Fatal("unbounded diff")
		}
		seen += len(page.Items)
		if page.Next == nil {
			break
		}
		after = *page.Next
	}
	if seen != 87 {
		t.Fatal("duplicate keys multiplied or pagination lost entries", seen)
	}
}

func TestFrozenContextMissingResponseAndProvenanceAreUnknown(t *testing.T) {
	s, db := contextFixture(t, map[string]any{"messages": []any{}}, map[string]any{"messages": []any{}})
	if _, err := db.Exec(`UPDATE model_generation SET status='pending',result_json=NULL WHERE step_index=1;INSERT INTO context_compaction VALUES('run',0,'{"stage":"summary_request","compaction":{"archive_ref":"artifact:history","source_refs":["trace:old"]},"omitted_images":["artifact:image"]}');INSERT INTO run_recall VALUES('run',0,'[{"source_type":"memory","source_ref":"memory:old","content":"frozen fact"}]');`); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"response", "provenance", "recall", "tools"} {
		out, err := s.Context(context.Background(), observability.ContextQuery{RunID: "run", Step: 1, Section: section})
		if err != nil || out.Selected != nil || len(out.Items) != 0 {
			t.Fatal(out, err)
		}
	}
	for section, marker := range map[string]string{"provenance": "artifact:history", "recall": "memory:old", "response": "call"} {
		out, err := s.Context(context.Background(), observability.ContextQuery{RunID: "run", Step: 0, Section: section, JSON: true})
		if err != nil || !strings.Contains(out.Content, marker) {
			t.Fatal(out, err)
		}
	}
}

func TestFrozenContextUsesExplicitReusedRecallReference(t *testing.T) {
	s, db := contextFixture(t, map[string]any{"messages": []any{}}, map[string]any{"messages": []any{}})
	if _, err := db.Exec(`INSERT INTO run_recall VALUES('run',0,'[{"source_type":"memory","source_ref":"memory:first","content":"frozen fact"}]');INSERT INTO context_compaction VALUES('run',1,'{"stage":"working_set","recall_step":0}');`); err != nil {
		t.Fatal(err)
	}
	out, err := s.Context(context.Background(), observability.ContextQuery{RunID: "run", Step: 1, Section: "recall", JSON: true})
	if err != nil || !strings.Contains(out.Content, "memory:first") {
		t.Fatal("continuation lost the exact frozen recall", out, err)
	}
}

func TestFrozenContextMarksSealedMemoryWithoutChangingStoredJSON(t *testing.T) {
	envelope := map[string]any{"sealed": "PRIVATE_CIPHERTEXT", "binding": "memory-injected:run", "encoded_bytes": 660}
	messages := []any{
		map[string]any{"role": "user", "text": "", "memory_text": envelope},
		map[string]any{"role": "user", "text": "Current request", "memory_text": envelope},
		map[string]any{"role": "assistant", "text": "", "tool_calls": []any{map[string]string{"id": "call", "name": "echo", "arguments": "{}"}}},
	}
	request := map[string]any{"messages": messages}
	s, db := contextFixture(t, request, request)
	var before string
	if err := db.QueryRow(`SELECT request_json FROM model_generation WHERE step_index=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i, message := range messages {
		q := observability.ContextQuery{RunID: "run", Step: 1, Section: "messages", Item: i}
		view, err := s.Context(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && (!strings.Contains(view.Content, "Encrypted memory content") || strings.Contains(view.Content, "PRIVATE_CIPHERTEXT")) {
			t.Fatal("sealed content looked empty or exposed ciphertext", view)
		}
		if i == 1 && !strings.HasPrefix(view.Content, "Current request\n") || i == 2 && view.Content != "" {
			t.Fatal("inspection changed ordinary message text", view)
		}
		q.JSON = true
		stored, err := s.Context(context.Background(), q)
		want, _ := json.Marshal(message)
		if err != nil || stored.Content != string(want) {
			t.Fatal("JSON no longer represents the stored message", stored, err)
		}
	}
	var after string
	if err := db.QueryRow(`SELECT request_json FROM model_generation WHERE step_index=1`).Scan(&after); err != nil || after != before {
		t.Fatal("inspection rewrote frozen input", err)
	}
}
