package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/llm"
)

func TestCodingCheckReviewRetainsOriginalIntentAndHonestMetrics(t *testing.T) {
	for _, priced := range []bool{false, true} {
		t.Run(fmt.Sprint(priced), func(t *testing.T) {
			s, r, a, p, _ := codingCheckFixture(t)
			installCodingPlugin(t, s, r)
			if priced {
				r.ConfigureSpend(spendPolicy(t, 0))
			}
			p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				result := llm.GenerationResult{Text: "prepared"}
				if priced {
					result.Usage = &llm.TokenUsage{InputTokens: 3, OutputTokens: 5}
				}
				return result, nil
			})
			mustSubmit(t, a, "review-facts", "prepare review facts")
			mustDrain(t, a)
			var ref string
			if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 34; i++ {
				if _, err := s.db.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) SELECT ?,task_id,task_epoch,?,'[]',? FROM task WHERE task_ref=?`, fmt.Sprint(i), fmt.Sprintf("accepted change %02d", i), time.Now().UnixMicro(), ref); err != nil {
					t.Fatal(err)
				}
			}
			current, err := s.CodeReport(context.Background(), ref, true)
			if err != nil {
				t.Fatal(err)
			}
			facts := current.Review
			if facts == nil || facts.OriginalTask != "Execute the requested tool work and return evidence." || facts.TaskStatus != "completed" || facts.ObservedAt <= 0 || facts.TaskEpoch != 1 || !facts.InstructionsTruncated || len(facts.Instructions) != 32 || facts.Instructions[0].Text != "accepted change 02" || facts.Instructions[31].Text != "accepted change 33" || len(facts.Guide) != 5 {
				t.Fatal("intent/guide was lost", facts)
			}
			m := facts.Metrics
			if m.Runs != 1 || m.ModelRequests != 1 || m.UsageRecords != 1 || m.UsageUnavailable != 0 || m.TaskWallMS < 0 {
				t.Fatal("wrong scoped observations", m)
			}
			if priced {
				if m.InputTokensReported == nil || *m.InputTokensReported != 3 || m.OutputTokensReported == nil || *m.OutputTokensReported != 5 || m.InputTokensMissing != 0 || m.OutputTokensMissing != 0 || len(m.Priced) != 1 || m.Priced[0].Currency != "USD" || m.Priced[0].ActualMicros != 8 || m.Priced[0].Generations != 1 || m.UnpricedUsage != 0 {
					t.Fatal("actual price or usage changed", m)
				}
			} else if m.InputTokensReported != nil || m.OutputTokensReported != nil || m.InputTokensMissing != 1 || m.OutputTokensMissing != 1 || len(m.Priced) != 0 || m.UnpricedUsage != 1 {
				t.Fatal("missing usage invented zeros/prices", m)
			}
			saved, err := s.SaveCodeReport(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) SELECT 'later',task_id,task_epoch,'later requirement','[]',? FROM task WHERE task_ref=?`, time.Now().UnixMicro(), ref); err != nil {
				t.Fatal(err)
			}
			var retained coding.Report
			if err = json.Unmarshal([]byte(readArtifact(t, s, artifactOwner(t, s), saved.Ref)), &retained); err != nil || retained.Review.InstructionSequence != facts.InstructionSequence || strings.Contains(fmt.Sprint(retained.Review.Instructions), "later requirement") {
				t.Fatal("retained review rebound", retained.Review, err)
			}
		})
	}
}

func TestCodingCheckReviewRejectsInstructionChangeDuringArtifactSave(t *testing.T) {
	s, r, a, p, _ := codingCheckFixture(t)
	installCodingPlugin(t, s, r)
	p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "prepared"}, nil
	})
	mustSubmit(t, a, "instruction-fence", "prepare")
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	changed := false
	s.MainBoundary = func(point string) {
		if point != "before_code_report_commit" {
			return
		}
		changed = true
		if _, err := s.db.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) SELECT 'new-criteria',task_id,task_epoch,'require a new behavior','[]',? FROM task WHERE task_ref=?`, time.Now().UnixMicro(), ref); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.SaveCodeReport(context.Background(), ref)
	if !changed || !errors.Is(err, agent.ErrConflict) || saved.Ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
		t.Fatal("saved an out-of-date acceptance package", changed, saved, err)
	}
}
