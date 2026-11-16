package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestScheduleGuidanceFreezesRealMainAcrossUpgradeAndWithdrawal(t *testing.T) {
	s, r, a, p := automationFixture(t)
	ctx := context.Background()
	h := installScheduleOwnerMethod(t, s, r)
	firstGraph, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Schedule method 1.0.1. "
	upgraded := false
	seen := 0
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		var spec *llm.ToolSpec
		for i := range req.Tools {
			if req.Tools[i].Name == automation.ManageName {
				spec = &req.Tools[i]
			}
		}
		if spec == nil || !strings.HasPrefix(spec.Description, expected) || spec.InputSchema != automation.Schema {
			t.Fatal("feature wording or fixed authority drift", spec, expected)
		}
		seen++
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "listed"}, nil
		}
		if !upgraded {
			installScheduleMethodVersion(t, s, r, "1.0.2", 4)
			upgraded = true
		}
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "list-guidance", Name: automation.ManageName, Arguments: `{"operation":"list"}`}}}, nil
	}
	first := mustSubmit(t, a, "guidance-first", "List original arrangements")
	var original string
	if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, first.RunID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	var originalSpecs []tool.Spec
	if err := json.Unmarshal([]byte(original), &originalSpecs); err != nil {
		t.Fatal(err)
	}
	for _, spec := range originalSpecs {
		if spec.Name == automation.ManageName && (spec.Permission != "task_control" || spec.ReplayPolicy != "safe" || spec.InputSchema != automation.Schema || spec.Version != 1 || spec.ResourceHash == "") {
			t.Fatal("frozen authority changed", spec)
		}
	}
	mustDrain(t, a)
	if !upgraded || seen != 2 {
		t.Fatal("actual Main/tool continuation did not run", seen)
	}
	expected = "Schedule method 1.0.2. "
	mustSubmit(t, a, "guidance-next", "List with the new method")
	mustDrain(t, a)
	var retained string
	if err := s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, first.RunID).Scan(&retained); err != nil || retained != original {
		t.Fatal("upgrade rewrote original capabilities", err)
	}
	mustSubmit(t, a, "guidance-retained-before-rollback", "Retain this selected version")
	selected, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	activateScheduleMethod(t, h, "guidance-exact-rollback", selected.Revision, firstGraph.Revision, 2)
	// This queued Run keeps version2 even after current selection rolls back.
	mustDrain(t, a)
	expected = "Schedule method 1.0.1. "
	mustSubmit(t, a, "guidance-rolled-back", "Use the exact restored original method")
	mustDrain(t, a)
	current, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "guidance-withdraw", current.Revision); err != nil {
		t.Fatal(err)
	}
	// A withdrawn contribution stays blank, retaining only fixed control authority.
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		found := false
		for _, spec := range req.Tools {
			if spec.Name == automation.ManageName {
				found = true
			}
			if spec.Name == automation.ManageName && (spec.Description != "" || spec.InputSchema != automation.Schema) {
				t.Fatal("withdrawal restored native wording or changed authority", spec)
			}
		}
		if !found {
			t.Fatal("fixed controls disappeared with wording")
		}
		return llm.GenerationResult{Text: "control retained"}, nil
	}
	third := mustSubmit(t, a, "guidance-withdrawn", "Inspect retained controls")
	mustDrain(t, a)
	var raw string
	if err = s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, third.RunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var specs []tool.Spec
	if err = json.Unmarshal([]byte(raw), &specs); err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if spec.Name == automation.ManageName && (spec.Description != "" || spec.Permission != "task_control" || spec.ReplayPolicy != "safe" || spec.InputSchema != automation.Schema || spec.ResourceHash != "") {
			t.Fatal("native fallback stored")
		}
	}
	if err = s.db.QueryRow(`SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, first.RunID).Scan(&retained); err != nil || retained != original {
		t.Fatal("lifecycle rewrote original frozen metadata", err)
	}

}

func TestScheduleGuidanceForgetDuringColdPreparationDoesNotResubmitIngress(t *testing.T) {
	s, r, _, _ := automationFixture(t)
	ctx := context.Background()
	installScheduleOwnerMethod(t, s, r)
	m, err := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r.ConfigureMemory(m, memory.Vault{})
	reached := false
	s.MainBoundary = func(point string) {
		if point == "before_schedule_guidance_retry" && !reached {
			reached = true
			receipt, err := r.ManageMemory(ctx, "schedule-guidance-forget", memory.Mutation{Operation: "forget", Category: "health"})
			if err != nil || receipt["accepted"] != true {
				t.Fatal(receipt, err)
			}
		}
	}
	private := "Original schedule ingress must not survive withdrawn privacy"
	_, err = r.Submit(ctx, "guidance-private", conversation.Text(private))
	s.MainBoundary = nil
	if !reached || !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("cold metadata retry did not reject original ingress", err)
	}
	if scalar(t, s, `SELECT count(*) FROM run`) != 0 || scalar(t, s, `SELECT count(*) FROM conversation_intent WHERE instr(message_json,?)>0`, private) != 0 {
		t.Fatal("metadata preparation resurrected private ingress")
	}
}
