package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestContextPureSelectionInterruptedBeforeAcceptanceRecoversOnOriginalGraph(t *testing.T) {
	for _, point := range []string{"after_context_policy_read", "after_compaction_commit"} {
		t.Run(point, func(t *testing.T) {
			s, r, a, p := childFixture(t)
			h := installContextPlugin(t, s, r)
			p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "original answer completed"}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			crossed := false
			s.MainBoundary = func(name string) {
				if name == point {
					crossed = true
					cancel()
				}
			}
			receipt := mustSubmit(t, a, "pure-context-interruption", "original authorized input")
			claim, err := r.Claim(context.Background(), receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Initialize(context.Background(), claim); err != nil {
				t.Fatal(err)
			}
			run, err := r.Load(context.Background(), claim)
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := r.Steering(context.Background(), claim, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range inputs {
				run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{MessageID: input.Message.MessageID, Type: "user", Content: input.Message.Content})
				run.Checkpoint.InputSequence = input.Sequence
			}
			if err = r.Checkpoint(context.Background(), claim, run.Version, run.Checkpoint); err != nil {
				t.Fatal(err)
			}
			run, err = r.Load(context.Background(), claim)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := h.Store.Binding(context.Background(), receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.Generation(ctx, claim, run)
			if !errors.Is(err, context.Canceled) || !crossed {
				t.Fatal("real context interruption not reached", crossed, err)
			}
			if got := scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='context-compact'`, receipt.RunID); got != 0 {
				t.Fatal("pure interrupted computation created opaque outcome", got)
			}
			want := 0
			if point == "after_compaction_commit" {
				want = 1
			}
			if got := scalar(t, s, `SELECT count(*) FROM context_compaction WHERE run_id=?`, receipt.RunID); got != want {
				t.Fatal("context acceptance boundary wrong", got, want)
			}
			var before string
			if want == 1 {
				if err = s.db.QueryRow(`SELECT messages_json FROM context_compaction WHERE run_id=?`, receipt.RunID).Scan(&before); err != nil {
					t.Fatal(err)
				}
			}
			s.MainBoundary = nil
			if err = r.Release(context.Background(), claim); err != nil {
				t.Fatal(err)
			}
			// Recovery itself must finish the affected original Run without selecting,
			// editing or reloading any graph. The accepted context remains authoritative.
			mustDrain(t, a)
			after, err := h.Store.Binding(context.Background(), receipt.RunID)
			if err != nil || after != selected {
				t.Fatal("recovery rebound original Run", after, selected, err)
			}
			if got := scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='completed'`, receipt.RunID); got != 1 {
				t.Fatal("original dependent Run did not finish immediately after recovery")
			}
			if len(p.requests) != 1 {
				t.Fatal("model request repeated", len(p.requests))
			}
			if want == 1 {
				var after string
				if err = s.db.QueryRow(`SELECT messages_json FROM context_compaction WHERE run_id=?`, receipt.RunID).Scan(&after); err != nil || after != before {
					t.Fatal("accepted context changed on recovery", err)
				}
			}
		})
	}
}

func TestContextPureSelectionKeepsLegacyCompletedAndUnknownCalls(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "completed"}[completed], func(t *testing.T) {
			ctx := context.Background()
			s, r, a, _ := childFixture(t)
			h := installContextPlugin(t, s, r)
			receipt := mustSubmit(t, a, "historical-context-call", "original input")
			claim, err := r.Claim(ctx, receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Release(ctx, claim)
			if err = r.Initialize(ctx, claim); err != nil {
				t.Fatal(err)
			}
			run, err := r.Load(ctx, claim)
			if err != nil {
				t.Fatal(err)
			}
			facts := contract.Facts{Messages: []contract.Message{{Role: "user", Input: true, Protected: true, Full: contract.Cost{Model: 200, Frame: 200}, Images: []contract.Image{}}}, ModelBytes: 5000, FrameBytes: 5000, Recent: 6, SummaryBytes: 2048, ImageLimit: 8}
			data, _ := json.Marshal(facts)
			input := json.RawMessage(`{"input_id":"facts"}`)
			key := "context-select:0"
			var expected []byte
			if completed {
				expected, err = h.CallWithInput(ctx, rsiplugin.Execution{RunID: receipt.RunID}, "context-compact", contract.Contract, key, input, []rsi.InputData{{ID: "facts", Data: data}})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				descriptor := []rsiplugin.Input{{ID: "facts", Size: int64(len(data)), Digest: rsiplugin.FileDigest(data)}}
				hash, e := rsiplugin.Digest("steward.rsi.input/1", struct {
					Contract string
					Input    json.RawMessage
					Inputs   []rsiplugin.Input `json:",omitempty"`
				}{contract.Contract, input, descriptor})
				if e != nil {
					t.Fatal(e)
				}
				if _, _, err = h.Store.BeginCall(ctx, receipt.RunID, "context-compact", key, hash); err != nil {
					t.Fatal(err)
				}
			}
			crossed := false
			s.MainBoundary = func(point string) {
				if point == "after_context_policy_read" {
					crossed = true
				}
			}
			defer func() { s.MainBoundary = nil }()
			value, err := r.readContextSelection(ctx, claim, run, data)
			if completed {
				if err != nil || !reflect.DeepEqual(value, expected) {
					t.Fatal("historical accepted selection not reused", err)
				}
			} else if !errors.Is(err, rsi.ErrUnknown) || len(value) != 0 {
				t.Fatal("unknown opaque call was replayed", err)
			}
			if crossed || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='context-compact'`, receipt.RunID) != 1 {
				t.Fatal("legacy boundary escaped to pure reevaluation")
			}
			if completed {
				changed := append([]byte(nil), data...)
				changed = append(changed, ' ')
				if _, err = r.readContextSelection(ctx, claim, run, changed); !errors.Is(err, rsi.ErrConflict) {
					t.Fatal("historical input identity fence lost", err)
				}
			}
		})
	}
}

func TestContextPureSelectionLateAuthorityChangesRejectBeforeAcceptance(t *testing.T) {
	for _, cause := range []string{"release", "stop", "quarantine", "forget"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, _ := childFixture(t)
			h := installContextPlugin(t, s, r)
			receipt := mustSubmit(t, a, "late-context-fence", "original input")
			claim, err := r.Claim(ctx, receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Release(ctx, claim)
			if err = r.Initialize(ctx, claim); err != nil {
				t.Fatal(err)
			}
			run, err := r.Load(ctx, claim)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := h.Store.Binding(ctx, receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			crossed := false
			s.MainBoundary = func(point string) {
				if point != "after_context_policy_read" {
					return
				}
				crossed = true
				switch cause {
				case "release":
					err = r.Release(ctx, claim)
				case "stop":
					err = h.Store.Stop(ctx, true)
				case "quarantine":
					err = h.Store.Quarantine(ctx, graph)
				case "forget":
					owner := idAgent(t, s)
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			defer func() { s.MainBoundary = nil }()
			data, _ := json.Marshal(contract.Facts{Messages: []contract.Message{}, ModelBytes: 5000, FrameBytes: 5000, Recent: 6, SummaryBytes: 2048, ImageLimit: 8})
			value, err := r.readContextSelection(ctx, claim, run, data)
			want := rsi.ErrRevoked
			if cause == "release" || cause == "forget" {
				want = agent.ErrClaimLost
			}
			if !crossed || !errors.Is(err, want) || len(value) != 0 {
				t.Fatal("late authority change accepted", crossed, err)
			}
			if scalar(t, s, `SELECT count(*) FROM context_compaction WHERE run_id=?`, receipt.RunID) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='context-compact'`, receipt.RunID) != 0 {
				t.Fatal("rejected read left accepted or opaque state")
			}
		})
	}
}
