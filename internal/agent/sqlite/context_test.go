package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestContinuationExcludesChildPrivateTrace(t *testing.T) {
	s, _, a, p := childFixture(t)
	const marker = "CHILD_INTERNAL_ONLY_REVIEW_PROBE"
	var continuation []llm.Message
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		var position string
		var epoch int
		if e := s.db.QueryRow(`SELECT coalesce(worker_position,''),coalesce(task_epoch,0) FROM run WHERE run_id=?`, req.RunID).Scan(&position, &epoch); e != nil {
			return llm.GenerationResult{}, e
		}
		last := req.Messages[len(req.Messages)-1]
		if position == "child" {
			if last.Role == "tool" {
				return llm.GenerationResult{Text: "child public final result"}, nil
			}
			return calls("mcp__fixture__echo", `{"text":"`+marker+`"}`), nil
		}
		if position == "root" {
			if epoch > 1 {
				continuation = req.Messages
				return llm.GenerationResult{Text: "continued public result"}, nil
			}
			if last.Role == "tool" {
				return llm.GenerationResult{Text: "root aggregate public final only"}, nil
			}
			return calls("builtin__agent_run", `{"task":"child explicit task"}`), nil
		}
		if last.Role == "tool" || isFollowup(req) {
			return llm.GenerationResult{Text: "Main acknowledgement"}, nil
		}
		if strings.Contains(last.Text, "append") {
			var ref string
			if e := s.db.QueryRow(`SELECT task_ref FROM task LIMIT 1`).Scan(&ref); e != nil {
				return llm.GenerationResult{}, e
			}
			raw, _ := json.Marshal(task.ManageRequest{Operation: "append", TaskRef: ref, Instruction: "continue with new instruction"})
			return calls(task.ManageName, string(raw)), nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"root task with final-only child results"}`), nil
	}
	mustSubmit(t, a, "review-create", "create task")
	mustDrain(t, a)
	mustSubmit(t, a, "review-append", "append task")
	mustDrain(t, a)
	raw, _ := json.Marshal(continuation)
	found := false
	origin := ""
	for _, m := range continuation {
		if strings.Contains(m.Text, marker) {
			found = true
			origin = m.Origin
			t.Logf("imported role=%s origin=%q prefix=%.110s", m.Role, m.Origin, m.Text)
		}
	}
	t.Logf("continuation_messages=%d private_child_marker_imported=%v runtime_origin=%q", len(continuation), found, origin)
	if found {
		t.Fatalf("Child trace escaped its explicit result boundary: %s", raw)
	}
}

func TestContinuationPreservesUnknownExternalOutcome(t *testing.T) {
	s, r, a := controlFixture(t, nil)
	def := tool.Definition{Spec: tool.Spec{Name: "mcp__fixture__external_publish", Version: 1, InputSchema: `{"type":"object","properties":{},"additionalProperties":false}`, Permission: "external_effect", ReplayPolicy: "never", Risk: "high", Provider: "fixture", OutputBytes: 1024, TimeoutMS: 10000, ConflictPolicy: "none"}, Invoke: func(context.Context, tool.Request) tool.Outcome { return tool.Outcome{Ambiguous: true} }}
	defs := []tool.Definition{def}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	reg, e := tool.NewRegistry(defs)
	if e != nil {
		t.Fatal(e)
	}
	r.registry, a.Registry = reg, reg
	ctx := context.Background()
	controlMain(t, r, a, "unknown-create", "create")
	c := rootClaim(t, s, r)
	run, e := r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	gen, e := r.Generation(ctx, c, run)
	if e != nil {
		t.Fatal(e)
	}
	result := calls(def.Spec.Name, `{}`)
	if e = s.Models().RecordUsageAnd(ctx, gen.Request, result, 0, nil, s.AcceptGeneration); e != nil {
		t.Fatal(e)
	}
	ts := r.ToolStore(c)
	invs, e := ts.Accept(ctx, gen.Request.GenerationID, result.ToolCalls, run.Tools)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ts.(tool.PolicyStore).Prepare(ctx, invs[0].ID); e != nil {
		t.Fatal(e)
	}
	approvePolicy(t, s, r)
	if e = ts.Start(ctx, invs[0].ID); e != nil {
		t.Fatal(e)
	}
	controlMain(t, r, a, "unknown-pause", "pause")
	if stopped, e := r.SettleStop(ctx, c); e != nil || !stopped {
		t.Fatal(stopped, e)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='unknown'`, invs[0].ID) != 1 {
		t.Fatal("no unknown outcome")
	}
	controlMain(t, r, a, "unknown-append", "append")
	c2 := rootClaim(t, s, r)
	defer r.Release(ctx, c2)
	next, e := r.Load(ctx, c2)
	if e != nil {
		t.Fatal(e)
	}
	inputs, e := r.Steering(ctx, c2, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range inputs {
		next.Checkpoint.Messages = append(next.Checkpoint.Messages, agent.AgentMessage{Type: "user", Content: i.Message.Content})
		next.Checkpoint.InputSequence = i.Sequence
	}
	if e = r.Checkpoint(ctx, c2, next.Version, next.Checkpoint); e != nil {
		t.Fatal(e)
	}
	next, e = r.Load(ctx, c2)
	if e != nil {
		t.Fatal(e)
	}
	after, e := r.Generation(ctx, c2, next)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(after.Request.Messages)
	warned := strings.Contains(strings.ToLower(string(raw)), "unknown") || strings.Contains(strings.ToLower(string(raw)), "unconfirmed")
	t.Logf("old_unknown_invocation_count=1 continuation_warns_unknown=%v", warned)
	if !warned {
		t.Fatal("unknown outcome was omitted from continuation")
	}
}

func TestSemanticCompactionRecoversAcceptedSummaryWithoutToolReplay(t *testing.T) {
	testSemanticCompactionRecovery(t, false)
}

func testSemanticCompactionRecovery(t *testing.T, managed bool) {
	var selectionReads atomic.Int64
	s, r, _, a, p := loopFixture(t)
	if managed {
		installContextPlugin(t, s, r)
		s.MainBoundary = func(point string) {
			if point == "after_context_policy_read" {
				selectionReads.Add(1)
			}
		}
		defer func() { s.MainBoundary = nil }()
	}
	ctx := context.Background()
	summaryCalls := 0
	var userStream strings.Builder
	a.Provider = summaryStreamProvider{p}
	a.Sink = func(e agent.Event) { userStream.WriteString(e.Text) }
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[0].Text == prompt.SummarySystem() {
			summaryCalls++
			if len(req.Tools) != 0 {
				t.Fatal("summarizer received executable tools")
			}
			return llm.GenerationResult{Text: "Goal: finish the request\nConstraints and preferences: DO_NOT_PUBLISH\nProgress: prior evidence retained; external receipt RECEIPT_73 confirmed\nDecisions: preserve sources\nNext steps: complete remaining work\nCritical evidence: use the history Artifact."}, nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Business result"}, nil
		}
		return calls("mcp__fixture__echo", `{"text":"execute exactly once"}`), nil
	}
	rc := mustSubmit(t, a, "semantic-compaction", "finish with DO_NOT_PUBLISH")
	c, e := r.Claim(ctx, rc.RunID)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Initialize(ctx, c); e != nil {
		t.Fatal(e)
	}
	run, e := r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 25; i++ {
		id := jsonNumberForTest(i)
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{Type: "assistant", Content: []conversation.ContentPart{{Type: "tool_call", ToolCall: &conversation.ToolCall{ID: id, Name: "mcp__fixture__echo", Arguments: `{"text":"historical call"}`}}}}, agent.AgentMessage{Type: "tool_result", ToolCallID: id, Content: conversation.Text("RECEIPT_73 " + strings.Repeat("historical evidence ", 60))})
	}
	inputs, e := r.Steering(ctx, c, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, i := range inputs {
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{MessageID: i.Message.MessageID, Type: "user", Content: i.Message.Content})
		run.Checkpoint.InputSequence = i.Sequence
	}
	if e = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); e != nil {
		t.Fatal(e)
	}
	run, e = r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	gen, e := r.Generation(ctx, c, run)
	if e != nil || gen.Compaction == nil {
		t.Fatal("summary not admitted", e)
	}
	firstID := gen.Request.GenerationID
	if _, e = p.Generate(ctx, gen.Request, nil); e != nil {
		t.Fatal(e)
	}
	// Simulate death after accepted model output but before checkpoint application.
	if e = r.Release(ctx, c); e != nil {
		t.Fatal(e)
	}
	mustDrain(t, a)
	firstDispatches := 0
	seenGenerations := map[string]bool{}
	for _, request := range p.requests {
		if request.GenerationID == firstID {
			firstDispatches++
		}
		if seenGenerations[request.GenerationID] {
			t.Fatal("accepted generation dispatched again", request.GenerationID)
		}
		seenGenerations[request.GenerationID] = true
	}
	if managed && (selectionReads.Load() == 0 || scalar(t, s, `SELECT count(*) FROM context_compaction WHERE run_id=?`, rc.RunID) == 0) {
		t.Fatal("semantic summary bypassed plugin")
	}
	if summaryCalls < 1 || firstDispatches != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation`) != 1 {
		t.Fatal("summary/effects replayed", summaryCalls)
	}
	var raw string
	if e = s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, rc.RunID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var cp agent.Checkpoint
	if json.Unmarshal([]byte(raw), &cp) != nil || cp.WorkingState == nil {
		t.Fatal("missing durable working state")
	}
	scope := artifact.Scope{UserID: artifact.LocalUser, AgentID: run.AgentID, RunID: run.ID}
	f, e := s.Artifacts().Open(ctx, scope, cp.WorkingState.ArchiveRef)
	if e != nil {
		t.Fatal(e)
	}
	archived, e := io.ReadAll(f)
	f.Close()
	if e != nil || !strings.Contains(string(archived), "RECEIPT_73") {
		t.Fatal("original evidence cannot be reopened", e)
	}
	if scalar(t, s, `SELECT count(*) FROM artifact WHERE artifact_ref=? AND visibility='run'`, cp.WorkingState.ArchiveRef) != 1 {
		t.Fatal("private trace archive was made Task-visible")
	}
	sawState := false
	for _, req := range p.requests {
		if req.Snapshot.SnapshotID != run.Snapshot.SnapshotID {
			t.Fatal("model snapshot drift")
		}
		if req.GenerationID != firstID {
			for _, m := range req.Messages {
				if m.Source != nil && m.Source.Kind == "working_state" {
					sawState = true
				}
			}
		}
	}
	if !sawState {
		t.Fatal("summary was not used as runtime working context")
	}
	assertFrozenContextRun(t, s, run.ID)
	provenance, e := s.Inspector().Context(ctx, observability.ContextQuery{RunID: run.ID, Step: 0, Section: "provenance"})
	if e != nil || !strings.Contains(provenance.Content, "summary_request") || !strings.Contains(provenance.Content, "history_artifact") {
		t.Fatal("semantic compaction provenance not inspectable", provenance, e)
	}
	if strings.Contains(userStream.String(), "PRIVATE_SUMMARY_DELTA") || !strings.Contains(userStream.String(), "PUBLIC_RESULT_DELTA") {
		t.Fatal("internal summary stream reached user delivery", userStream.String())
	}
	// A different Main cannot read private execution history until the explicit,
	// privacy-checked business handoff grants that one reference.
	next := mustSubmit(t, a, "semantic-followup", "continue this work")
	nextScope := scope
	nextScope.RunID = next.RunID
	if _, e = s.Artifacts().Describe(ctx, nextScope, cp.WorkingState.ArchiveRef); e != artifact.ErrNotFound {
		t.Fatal("private history leaked before handoff", e)
	}
	c2, e := r.Claim(ctx, next.RunID)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Release(ctx, c2)
	if e = r.Initialize(ctx, c2); e != nil {
		t.Fatal(e)
	}
	continued, e := r.Load(ctx, c2)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(continued.Checkpoint.Messages)
	if !strings.Contains(string(b), "previous_main_state") || !strings.Contains(string(b), cp.WorkingState.ArchiveRef) {
		t.Fatal("Main business continuity missing")
	}
	if _, e = s.Artifacts().Describe(ctx, nextScope, cp.WorkingState.ArchiveRef); e != nil {
		t.Fatal("explicit history grant missing", e)
	}
	rows, e := s.db.Query(`SELECT artifact_ref FROM artifact WHERE run_id=? AND put_key LIKE 'context:%'`, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	archives := []string{}
	for rows.Next() {
		var ref string
		if e = rows.Scan(&ref); e != nil {
			t.Fatal(e)
		}
		archives = append(archives, ref)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Fatal(e)
	}
	if len(archives) < 2 {
		t.Fatal("multiple summary chunks were not exercised")
	}
	for _, ref := range archives {
		if _, e = s.Artifacts().Describe(ctx, nextScope, ref); e != nil {
			t.Fatal("prior summary chunk cannot be reopened", e)
		}
	}

}

type summaryStreamProvider struct{ *loopProvider }

func (p summaryStreamProvider) Generate(ctx context.Context, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	if sink != nil {
		text := "PUBLIC_RESULT_DELTA"
		if r.Messages[0].Text == prompt.SummarySystem() {
			text = "PRIVATE_SUMMARY_DELTA"
		}
		sink(llm.GenerationEvent{Kind: "text_delta", Text: text})
	}
	return p.loopProvider.Generate(ctx, r, sink)
}
func (p summaryStreamProvider) Continue(ctx context.Context, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return p.Generate(ctx, r, sink)
}

func TestContextArchiveIsPrivateWithinTaskTree(t *testing.T) {
	s, r, a := controlFixture(t, nil)
	ctx := context.Background()
	controlMain(t, r, a, "archive-create", "create")
	c := rootClaim(t, s, r)
	defer r.Release(ctx, c)
	run, e := r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 32; i++ {
		run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.TextMessage("assistant", strings.Repeat("private root working notes ", 40)))
	}
	if e = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); e != nil {
		t.Fatal(e)
	}
	run, e = r.Load(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	gen, e := r.Generation(ctx, c, run)
	if e != nil || gen.Compaction == nil {
		t.Fatal("missing summary archive", e)
	}
	addControlChild(t, s, c.RunID(), "archive-child")
	var scope artifact.Scope
	if e = r.write(ctx, func(tx *sql.Tx) error { var e error; scope, e = runScope(ctx, tx, "archive-child"); return e }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Artifacts().Describe(ctx, scope, gen.Compaction.ArchiveRef); e != artifact.ErrNotFound {
		t.Fatal("Child accessed private Parent history", e)
	}
}

func jsonNumberForTest(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestContextFormatAndWorkingStateBoundariesAreDurable(t *testing.T) {
	s, _, _, a, _ := loopFixture(t)
	rc := mustSubmit(t, a, "format-guard", "keep this request")
	for _, query := range []string{
		`UPDATE run SET checkpoint_json=json_set(checkpoint_json,'$.context_version',0),version=version+1 WHERE run_id=?`,
		`UPDATE run SET checkpoint_json=json_set(checkpoint_json,'$.working_state',json('{"through":999,"text":"invented","source_refs":[],"archive_ref":"artifact:missing"}')),version=version+1 WHERE run_id=?`,
	} {
		if _, e := s.db.Exec(query, rc.RunID); e == nil {
			t.Fatal("invalid context mutation accepted")
		}
	}
	var raw string
	if e := s.db.QueryRow(`SELECT checkpoint_json FROM run WHERE run_id=?`, rc.RunID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var cp agent.Checkpoint
	if json.Unmarshal([]byte(raw), &cp) != nil || cp.ContextVersion != 2 || cp.WorkingState != nil {
		t.Fatal("rejected mutation altered the checkpoint")
	}
}

func TestContinuationRetainsAcceptedRootFinalButNotSummary(t *testing.T) {
	for _, summary := range []bool{false, true} {
		name := "business_final"
		if summary {
			name = "internal_summary"
		}
		t.Run(name, func(t *testing.T) {
			s, r, a := controlFixture(t, nil)
			ctx := context.Background()
			controlMain(t, r, a, "accepted-create", "create")
			c := rootClaim(t, s, r)
			defer r.Release(ctx, c)
			run, e := r.Load(ctx, c)
			if e != nil {
				t.Fatal(e)
			}
			if summary {
				for i := 0; i < 32; i++ {
					run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.TextMessage("assistant", strings.Repeat("old working evidence ", 60)))
				}
				if e = r.Checkpoint(ctx, c, run.Version, run.Checkpoint); e != nil {
					t.Fatal(e)
				}
				run, e = r.Load(ctx, c)
				if e != nil {
					t.Fatal(e)
				}
			}
			gen, e := r.Generation(ctx, c, run)
			if e != nil || (gen.Compaction != nil) != summary {
				t.Fatal("unexpected generation purpose", e)
			}
			marker := "ACCEPTED_ROOT_OUTPUT_BEFORE_REPORT"
			if e = s.Models().RecordUsageAnd(ctx, gen.Request, llm.GenerationResult{Text: marker}, 0, nil, s.AcceptGeneration); e != nil {
				t.Fatal(e)
			}
			if scalar(t, s, `SELECT count(*) FROM worker_result WHERE run_id=?`, run.ID) != 0 {
				t.Fatal("fixture passed the report boundary")
			}
			controlMain(t, r, a, "accepted-pause", "pause")
			if stopped, e := r.SettleStop(ctx, c); e != nil || !stopped {
				t.Fatal(stopped, e)
			}
			controlMain(t, r, a, "accepted-append", "append")
			next := rootClaim(t, s, r)
			defer r.Release(ctx, next)
			continued, e := r.Load(ctx, next)
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(continued.Checkpoint.Messages)
			if strings.Contains(string(raw), marker) == summary {
				t.Fatal("accepted business final lost or internal summary exposed")
			}
		})
	}
}
