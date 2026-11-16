package sqlite

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/conversation/contract"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func installConversationPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	expected, _ := json.Marshal(contract.Policy{RolePolicy: strings.TrimPrefix(prompt.Main(), agent.MainIdentity), ExecutionPolicy: strings.TrimSuffix(prompt.MainExecution(), "\n</rules>")})
	return installRuntimeFeature(t, s, r, "conversation", rsi.Policy{ID: "conversation-owner-fixture", AllowedContracts: []string{contract.Contract}, Cases: []rsi.Case{{InstanceID: "conversation", Contract: contract.Contract, Input: json.RawMessage(`{"role":"main"}`), Expected: expected}}})
}

func TestConversationPluginRecoversPolicyBeforeContextCommit(t *testing.T) {
	ctx := context.Background()
	s, r, _, a, _ := loopFixture(t)
	installConversationPlugin(t, s, r)
	receipt := mustSubmit(t, a, "conversation-init-recovery", "original current input")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	if _, err = s.db.Exec(`CREATE TRIGGER fail_conversation_context BEFORE UPDATE ON run WHEN json_extract(OLD.checkpoint_json,'$.context_ready')=0 AND json_extract(NEW.checkpoint_json,'$.context_ready')=1 BEGIN SELECT RAISE(ABORT,'fixture context failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, c); err == nil {
		t.Fatal("context commit interruption not exercised")
	}
	var original string
	if err = s.db.QueryRow(`SELECT result_json FROM rsi_call WHERE run_id=? AND instance_id='conversation'`, receipt.RunID).Scan(&original); err != nil || original == "" {
		t.Fatal("policy was not durably accepted before checkpoint", err)
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, receipt.RunID) != 0 {
		t.Fatal("model admitted before policy context commit")
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_conversation_context`); err != nil {
		t.Fatal(err)
	}
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	run, err := r.Load(ctx, c)
	if err != nil || !run.Checkpoint.ContextReady {
		t.Fatal(err)
	}
	if _, ok := agent.FrozenSystemSections(run.Checkpoint.Messages); !ok {
		t.Fatal("recovery lost typed sections")
	}
	var recovered string
	if err = s.db.QueryRow(`SELECT result_json FROM rsi_call WHERE run_id=? AND instance_id='conversation'`, receipt.RunID).Scan(&recovered); err != nil || original != recovered {
		t.Fatal("recovery changed policy receipt", err)
	}
	if err = r.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	again, err := r.Load(ctx, c)
	if err != nil || !reflect.DeepEqual(again.Checkpoint, run.Checkpoint) || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='conversation'`, receipt.RunID) != 1 {
		t.Fatal("ready context reinterpreted", err)
	}
}

func TestConversationPluginKeepsToolContinuationPrefixAndDelivery(t *testing.T) {
	s, r, _, a, p := loopFixture(t)
	installConversationPlugin(t, s, r)
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "confirmed business delivery"}, nil
		}
		return calls("builtin__artifact_search", `{"text":"exact original evidence"}`), nil
	}
	receipt := mustSubmit(t, a, "conversation-tool-continuation", "complete this bounded action")
	mustDrain(t, a)
	if len(p.requests) != 2 || len(p.requests[1].Messages) <= len(p.requests[0].Messages) || !reflect.DeepEqual(p.requests[0].Messages, p.requests[1].Messages[:len(p.requests[0].Messages)]) {
		t.Fatal("method changed continuation prefix")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='conversation' AND result_json IS NOT NULL`, receipt.RunID) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND status='succeeded'`, receipt.RunID) != 1 {
		var details string
		_ = s.db.QueryRow(`SELECT coalesce(group_concat(tool_name||':'||status||':'||coalesce(error_json,'')),'') FROM tool_invocation WHERE run_id=?`, receipt.RunID).Scan(&details)
		t.Fatal("method/effect mismatch", scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='conversation' AND result_json IS NOT NULL`, receipt.RunID), details)
	}
	if scalar(t, s, `SELECT count(*) FROM delivery_request d JOIN main_reply m USING(delivery_id) WHERE m.run_id=?`, receipt.RunID) != 1 {
		t.Fatal("original delivery ownership changed")
	}
	assertFrozenContextRun(t, s, receipt.RunID)
}
