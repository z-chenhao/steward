package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

func TestTraceReadModelMainRootChildUsageAndBodies(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	main := mustSubmit(t, a, "trace-model", "Main input <img src=x>")
	mustDrain(t, a)
	reader := s.Inspector()
	before := countRows(t, s, "run_trace")
	list, err := reader.Traces(ctx, observability.TraceQuery{})
	if err != nil || len(list.Items) < 2 {
		t.Fatal(list, err)
	}
	for _, trace := range list.Items {
		created, e := time.Parse(time.RFC3339Nano, trace.CreatedAt)
		if e != nil {
			t.Fatal(e)
		}
		q := observability.TraceQuery{Search: strings.ToUpper(trace.ID), Session: trace.Session, ExecutionQuery: observability.ExecutionQuery{Role: trace.Role, Status: trace.Status, Since: created, Until: created.Add(time.Microsecond)}}
		found, e := reader.Traces(ctx, q)
		if e != nil || len(found.Items) != 1 || found.Items[0].ID != trace.ID {
			t.Fatal("search did not intersect identity/session/role/status/time", found, e)
		}
	}
	for _, search := range []string{"%", "' OR 1=1--"} {
		found, e := reader.Traces(ctx, observability.TraceQuery{Search: search})
		if e != nil || len(found.Items) != 0 {
			t.Fatal("search was not literal", found, e)
		}
	}
	filtered, e := reader.Traces(ctx, observability.TraceQuery{Search: "MAIN CONVERSATION", ExecutionQuery: observability.ExecutionQuery{Limit: 1}})
	if e != nil || len(filtered.Items) != 1 || filtered.Items[0].Role != "main" {
		t.Fatal("name search failed", filtered, e)
	}
	if filtered.Next != "" {
		next, e := reader.Traces(ctx, observability.TraceQuery{Search: "MAIN CONVERSATION", ExecutionQuery: observability.ExecutionQuery{Limit: 1, Cursor: filtered.Next}})
		if e != nil || len(next.Items) != 1 || next.Items[0].ID == filtered.Items[0].ID || next.Items[0].Role != "main" {
			t.Fatal("filtered keyset pagination failed", next, e)
		}
	}

	var activityCount int64
	for _, bucket := range list.Activity {
		activityCount += bucket.Count
	}
	if list.Total < int64(len(list.Items)) || activityCount != list.Total {
		t.Fatal("activity count does not match filtered traces", list.Total, activityCount)
	}
	if filtered.Total < int64(len(filtered.Items)) {
		t.Fatal("pagination changed total", filtered)
	}
	var filteredCount int64
	for _, bucket := range filtered.Activity {
		filteredCount += bucket.Count
	}
	if filteredCount != filtered.Total {
		t.Fatal("activity ignored search", filteredCount, filtered.Total)
	}
	var root string
	for _, trace := range list.Items {
		if trace.Role == "root" {
			root = trace.ID
		}
		if trace.Role == "child" {
			t.Fatal("child became independent trace")
		}
		if trace.Metrics.Generations == 0 {
			t.Fatal("missing generations")
		}
	}
	tree, err := reader.Observations(ctx, observability.ObservationQuery{RunID: root})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Trace.Parent != "" || tree.Trace.Root != root || tree.Selected.Type != "agent" {
		t.Fatal("trace ancestry changed", tree)
	}
	kinds := map[string]int{}
	gens := map[string]bool{}
	var toolID string
	for _, node := range tree.Items {
		kinds[node.Type]++
		if node.Type == "generation" {
			gens[node.Key] = true
			if node.Parent != "agent:"+node.RunID {
				t.Fatal("generation parent", node)
			}
			out, e := reader.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: observability.ObservationQuery{RunID: root, Selected: node.ID}, Field: "output"})
			if e != nil || !out.Available || !json.Valid([]byte(out.Content)) {
				t.Fatal(out, e)
			}
			messages, e := reader.ChatPreview(ctx, observability.ObservationQuery{RunID: root, Selected: node.ID})
			if e != nil || len(messages) == 0 {
				t.Fatal(messages, e)
			}
			wire, e := reader.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: observability.ObservationQuery{RunID: root, Selected: node.ID}, Field: "wire"})
			if e != nil || wire.Available {
				t.Fatal("missing dispatch inferred", wire, e)
			}
		}
		if node.Type == "tool" {
			toolID = node.ID
		}
	}
	if kinds["agent"] != 3 || kinds["generation"] < 3 || kinds["tool"] < 2 || kinds["chain"] < 3 {
		t.Fatal(kinds)
	}
	if int64(len(gens)) != tree.Trace.Metrics.Generations {
		t.Fatal("generation counted twice", len(gens), tree.Trace.Metrics)
	}
	if _, e := reader.Observations(ctx, observability.ObservationQuery{RunID: main.RunID, Selected: toolID}); !errors.Is(e, observability.ErrNotFound) {
		t.Fatal("cross-trace selection revealed", e)
	}
	if _, e := reader.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: observability.ObservationQuery{RunID: main.RunID, Selected: toolID}, Field: "input"}); !errors.Is(e, observability.ErrNotFound) {
		t.Fatal("cross-trace body revealed", e)
	}
	selected, e := reader.Observations(ctx, observability.ObservationQuery{RunID: root, Selected: toolID})
	if e != nil || selected.Selected.ID != toolID {
		t.Fatal(selected, e)
	}
	body, e := reader.ObservationBody(ctx, observability.BodyQuery{ObservationQuery: observability.ObservationQuery{RunID: root, Selected: toolID}, Field: "input"})
	if e != nil || !body.Available {
		t.Fatal(body, e)
	}
	if strings.Contains(body.Content, "owner_token") || strings.Contains(body.Content, "model_snapshot") {
		t.Fatal("internal ownership/config leaked")
	}
	if countRows(t, s, "run_trace") != before {
		t.Fatal("read wrote observation")
	}
}

func TestTraceObservationPaginationHasStableScopedKeys(t *testing.T) {
	ctx := context.Background()
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	main := mustSubmit(t, a, "trace-pagination", "read-only inspection")
	mustDrain(t, a)
	reader := s.Inspector()
	initial, err := reader.Observations(ctx, observability.ObservationQuery{RunID: main.RunID})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 220; i++ {
		if _, err = s.db.Exec(`INSERT INTO run_trace VALUES(?,?,?,100)`, main.RunID, 10000+i, `{"kind":"agent_start","step_index":0}`); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		page, e := reader.Observations(ctx, observability.ObservationQuery{RunID: main.RunID, Cursor: cursor})
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 100 {
			t.Fatal("unbounded observations")
		}
		for _, o := range page.Items {
			if seen[o.ID] {
				t.Fatal("duplicate observation")
			}
			seen[o.ID] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != len(initial.Items)+220 {
		t.Fatal("pagination omitted rows", len(seen))
	}
	var root string
	if err = s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if _, e := reader.Observations(ctx, observability.ObservationQuery{RunID: root, Cursor: cursor}); !errors.Is(e, observability.ErrInvalid) {
		t.Fatal("cursor crossed trace", e)
	}
}

func TestTraceMissingHTTPTerminalRemainsUnknownAfterCompletion(t *testing.T) {
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	main := mustSubmit(t, a, "incomplete-http", "inspect a completed trace")
	mustDrain(t, a)
	if _, err := s.db.Exec(`INSERT INTO dispatch_observation SELECT generation_id,'incomplete','started',created_at,'test',NULL,'','',0,'' FROM model_generation WHERE run_id=? LIMIT 1`, main.RunID); err != nil {
		t.Fatal(err)
	}
	tree, err := s.Inspector().Observations(context.Background(), observability.ObservationQuery{RunID: main.RunID, Selected: "dispatch:incomplete"})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Selected.Status != "unknown" || tree.Selected.Ended != nil || tree.Selected.Detail != "Outcome not recorded" {
		t.Fatalf("missing terminal evidence misrepresented as a running/completed request: %+v", tree.Selected)
	}
}

func TestTraceTTFTUsesFirstGenerationOutputAndPreservesMissingEvidence(t *testing.T) {
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	main := mustSubmit(t, a, "first-token", "Inspect first model output")
	mustDrain(t, a)
	ctx := context.Background()
	reader := s.Inspector()
	read := func() *int64 {
		t.Helper()
		traces, err := reader.Traces(ctx, observability.TraceQuery{Search: main.RunID})
		if err != nil || len(traces.Items) != 1 {
			t.Fatal(traces, err)
		}
		return traces.Items[0].Metrics.TTFTMS
	}
	if read() != nil {
		t.Fatal("missing timing became zero TTFT")
	}
	var generation string
	if err := s.db.QueryRow(`SELECT generation_id FROM model_generation WHERE run_id=? ORDER BY created_at,generation_id LIMIT 1`, main.RunID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	for _, mark := range []struct {
		attempt, phase string
		at             int64
	}{
		{"retry-one", "started", 1000000}, {"retry-one", "error", 1100000},
		{"retry-two", "started", 1500000}, {"retry-two", "headers", 1600000},
		{"retry-two", "first_body", 1700000}, {"retry-two", "first_text", 2691000},
	} {
		if _, err := s.db.Exec(`INSERT INTO dispatch_observation VALUES(?,?,?,?,'test',NULL,'','',0,'')`, generation, mark.attempt, mark.phase, mark.at); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(); got == nil || *got != 1691 {
		t.Fatal("TTFT ignored retries or used headers/first transport data", got)
	}
	if _, err := s.db.Exec(`INSERT INTO dispatch_observation VALUES(?,'retry-two','first_tool_call',1800000,'test',NULL,'','',0,'')`, generation); err != nil {
		t.Fatal(err)
	}
	if got := read(); got == nil || *got != 800 {
		t.Fatal("TTFT excluded tool-call output before text", got)
	}
	if _, err := s.db.Exec(`DELETE FROM dispatch_observation WHERE phase='first_text'`); err != nil {
		t.Fatal(err)
	}
	if got := read(); got == nil || *got != 800 {
		t.Fatal("tool-only generation has no TTFT", got)
	}
}

func TestTraceActivityRangesMatchFilteredRowsAtExclusiveBoundaries(t *testing.T) {
	s, _, a, p := childFixture(t)
	p.respond = childPolicy
	mustSubmit(t, a, "histogram", "Inspect histogram boundaries")
	mustDrain(t, a)
	reader := s.Inspector()
	ctx := context.Background()
	all, err := reader.Traces(ctx, observability.TraceQuery{})
	if err != nil || len(all.Items) == 0 {
		t.Fatal(all, err)
	}
	trace := all.Items[0]
	at, err := time.Parse(time.RFC3339Nano, trace.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	q := observability.TraceQuery{Search: trace.ID, ExecutionQuery: observability.ExecutionQuery{Since: at, Until: at.Add(time.Second)}}
	data, err := reader.Traces(ctx, q)
	if err != nil || data.Total != 1 || len(data.Activity) == 0 {
		t.Fatal(data, err)
	}
	var total int64
	for _, bucket := range data.Activity {
		total += bucket.Count
		start, e := time.Parse(time.RFC3339Nano, bucket.At)
		if e != nil {
			t.Fatal(e)
		}
		end, e := time.Parse(time.RFC3339Nano, bucket.Until)
		if e != nil {
			t.Fatal(e)
		}
		if start.Before(q.Since) || end.After(q.Until) || !start.Before(end) {
			t.Fatal("bucket escaped exact filter", bucket, q)
		}
		selected := q
		selected.Since, selected.Until = start, end
		rows, e := reader.Traces(ctx, selected)
		if e != nil || rows.Total != bucket.Count {
			t.Fatal("bucket and table diverged", bucket, rows, e)
		}
	}
	if total != data.Total {
		t.Fatal(total, data.Total)
	}
	q.Since = at.Add(-time.Second)
	q.Until = at
	empty, err := reader.Traces(ctx, q)
	if err != nil || empty.Total != 0 || len(empty.Activity) == 0 {
		t.Fatal("exclusive end included a boundary event or discarded empty time range", empty, err)
	}
	for _, bucket := range empty.Activity {
		if bucket.Count != 0 {
			t.Fatal(bucket)
		}
	}
}
