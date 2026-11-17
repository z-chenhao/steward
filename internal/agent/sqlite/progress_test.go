package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/prompt"
	"github.com/z-chenhao/assistant/internal/task"
)

func TestRootLoopPublishesStageUpdatesWithoutMainWork(t *testing.T) { testRootLoopProgress(t, false) }
func TestProgressPluginRootLoopProgress(t *testing.T)               { testRootLoopProgress(t, true) }
func testRootLoopProgress(t *testing.T, managed bool) {
	s, r, a, p := taskFixture(t)
	if managed {
		installProgressPlugin(t, s, r)
	}
	step, version := 0, 0
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if !isRoot(req) {
			if isFollowup(req) || req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "Main answer"}, nil
			}
			result := calls(task.ManageName, `{"operation":"create","task":"Compare venue constraints"}`)
			result.Text = "MAIN_PRIVATE_TEXT"
			return result, nil
		}
		if step > 0 {
			want := "Constraints reviewed; comparing available venues."
			if step == 4 {
				want = "Comparison saved; checking the recommendation."
			}
			assertProgress(t, s, want)
			current := scalar(t, s, `SELECT version FROM task`)
			if step == 1 {
				version = current
			} else if (step == 2 || step == 3) && current != version {
				t.Fatal("identical progress churned Task version", current, version)
			} else if step == 4 && current != version+1 {
				t.Fatal("new stage did not advance Task version once", current, version)
			}
			if scalar(t, s, `SELECT count(*) FROM task_update`) != 0 || scalar(t, s, `SELECT count(*) FROM main_inbox`) != 0 {
				t.Fatal("ordinary progress notified or woke Main")
			}
			items, err := r.Activity(context.Background())
			if err != nil || len(items) != 1 || !strings.Contains(items[0].ProgressSummary, "Working. Last update: "+want) {
				t.Fatal("activity did not show current phase and semantic summary", items, err)
			}
		}
		step++
		if step == 5 {
			return llm.GenerationResult{Text: "FINAL_BUSINESS_RESULT"}, nil
		}
		result := calls("mcp__fixture__echo", `{"text":"PRIVATE_TOOL_PAYLOAD"}`)
		result.Text = "Constraints reviewed; comparing available venues."
		if step == 3 {
			result.Text = strings.Repeat("界", 171)
		}
		if step == 4 {
			result.Text = "Comparison saved; checking the recommendation."
		}
		return result, nil
	}
	mustSubmit(t, a, "stages", "Delegate this comparison")
	mustDrain(t, a)
	assertProgress(t, s, "Comparison saved; checking the recommendation.")
	items, err := r.Activity(context.Background())
	if err != nil || len(items) != 1 || !strings.HasPrefix(items[0].ProgressSummary, "Completed.") {
		t.Fatal(items, err)
	}
	if scalar(t, s, `SELECT count(*) FROM task_update`) != 1 || scalar(t, s, `SELECT count(*) FROM main_inbox`) != 1 || scalar(t, s, `SELECT count(*) FROM delivery_request`) != 2 {
		t.Fatal("stage updates created extra reports or deliveries")
	}
}

func assertProgress(t *testing.T, s *Store, want string) {
	t.Helper()
	var text string
	if err := s.db.QueryRow(`SELECT progress_summary FROM task`).Scan(&text); err != nil || text != want {
		t.Fatalf("progress: got %q want %q: %v", text, want, err)
	}
}

func executeTaskRunID(t *testing.T, a agent.Actor, r *Runs, id string) error {
	t.Helper()
	c, err := r.Claim(context.Background(), id)
	if err != nil {
		return err
	}
	return executeTaskRun(t, a, r, context.Background(), c)
}

func TestProgressCheckpointAtomicRecoveryAndOwnership(t *testing.T) {
	testProgressAtomicRecovery(t, false)
}
func TestProgressPluginProgressAtomicRecovery(t *testing.T) { testProgressAtomicRecovery(t, true) }
func testProgressAtomicRecovery(t *testing.T, managed bool) {
	s, r, a, p := taskFixture(t)
	if managed {
		installProgressPlugin(t, s, r)
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			if req.Messages[len(req.Messages)-1].Role == "tool" {
				return llm.GenerationResult{Text: "Business result"}, nil
			}
			result := calls("mcp__fixture__echo", `{"text":"evidence"}`)
			result.Text = "Sources checked; preparing the comparison."
			return result, nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Accepted"}, nil
		}
		return calls(task.ManageName, `{"operation":"create","task":"Compare sources"}`), nil
	}
	main := mustSubmit(t, a, "atomic", "Delegate")
	if err := executeTaskRunID(t, a, r, main.RunID); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := r.Claim(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_progress BEFORE UPDATE OF progress_summary ON task BEGIN SELECT RAISE(ABORT,'progress unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err = executeTaskRun(t, a, r, ctx, c); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("checkpoint must fail with progress transaction", err)
	}
	assertProgress(t, s, "")
	if scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND json_extract(checkpoint_json,'$.phase')='model'`, id) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, id) != 0 {
		t.Fatal("progress failure left a partial checkpoint or executed tools")
	}
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=? AND status='completed'`, id) != 1 {
		t.Fatal("complete model response was lost")
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_progress`); err != nil {
		t.Fatal(err)
	}
	if managed && scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND operation_id LIKE 'task-progress:%' AND result_json IS NOT NULL`, id) != 2 {
		t.Fatal("guidance and accepted stage selection were not saved before the failed Task write")
	}
	// A different owner recovers the durable generation after the failed commit.
	if err = executeTaskRunID(t, a, r, id); err != nil {
		t.Fatal(err)
	}
	assertProgress(t, s, "Sources checked; preparing the comparison.")
	if scalar(t, s, `SELECT count(*) FROM model_generation WHERE run_id=?`, id) != 2 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=?`, id) != 1 {
		t.Fatal("recovery repeated the model turn or tool")
	}
	if managed && scalar(t, s, `SELECT count(*) FROM rsi_call WHERE run_id=? AND operation_id LIKE 'task-progress:%' AND result_json IS NOT NULL`, id) != 2 {
		t.Fatal("checkpoint recovery redispatched an already accepted stage selection")
	}
	// The old owner cannot overwrite a new owner's committed projection.
	if err = r.Progress(ctx, c, "stale"); !errors.Is(err, agent.ErrClaimLost) {
		t.Fatal("old owner updated progress", err)
	}
}

func TestProgressExcludesChildrenLegacyAndInvalidProse(t *testing.T) {
	testProgressExclusions(t, false)
}
func TestProgressPluginProgressExclusions(t *testing.T) { testProgressExclusions(t, true) }
func testProgressExclusions(t *testing.T, managed bool) {
	for _, tc := range []struct {
		name, text, want           string
		clipped, legacy, oldLayout bool
	}{
		{name: "empty"},
		{name: "whitespace", text: " \n\t "},
		{name: "too long", text: strings.Repeat("界", 171)},
		{name: "clipped", text: "incomplete", clipped: true},
		{name: "terminal controls", text: "\x1b[2JHidden instructions"},
		{name: "legacy", text: "LEGACY_PRIVATE_TEXT", legacy: true},
		{name: "frozen separate progress section", text: "Previously frozen stage", want: "Previously frozen stage", oldLayout: true},
		{name: "bounded", text: strings.Repeat("a", 512), want: strings.Repeat("a", 512)},
		{name: "normalized", text: "已核对来源。\n\t 正在比较。", want: "已核对来源。 正在比较。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, a, p := childFixture(t)
			if managed {
				installProgressPlugin(t, s, r)
			}
			p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				last := req.Messages[len(req.Messages)-1]
				if last.Role == "tool" || isFollowup(req) {
					return llm.GenerationResult{Text: "Business result"}, nil
				}
				if !isRoot(req) {
					return calls(task.ManageName, `{"operation":"create","task":"Root request"}`), nil
				}
				if last.Text == "Child request" {
					result := calls("mcp__fixture__echo", `{"text":"PRIVATE_CHILD_TOOL_DATA"}`)
					result.Text = "PRIVATE_CHILD_PROGRESS"
					return result, nil
				}
				result := calls(agent.RunToolName, `{"task":"Child request"}`)
				result.Text, result.Clipped = tc.text, tc.clipped
				return result, nil
			}
			main := mustSubmit(t, a, "prose", "Delegate")
			if err := executeTaskRunID(t, a, r, main.RunID); err != nil {
				t.Fatal(err)
			}
			if tc.legacy || tc.oldLayout {
				// Construct frozen progress policy before section consolidation:
				// either no opt-in or the previous standalone guidance section.
				var id string
				if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root'`).Scan(&id); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				claim, err := r.Claim(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				run, err := r.Load(ctx, claim)
				if err != nil {
					t.Fatal(err)
				}
				cp := run.Checkpoint
				for i := range cp.Messages {
					if cp.Messages[i].Type != "system" {
						continue
					}
					sections := []agent.SystemSection{}
					texts := []string{}
					for _, section := range cp.Messages[i].SystemSections {
						if section.Kind == "progress_guidance" {
							continue
						}
						if section.Kind == "execution" && section.Progress {
							section.Text = section.ChildText
							section.ChildText, section.Progress = "", false
						}
						sections = append(sections, section)
						texts = append(texts, section.Text)
						if tc.oldLayout && section.Kind == "execution" {
							sections = append(sections, agent.SystemSection{Kind: "progress_guidance", Text: prompt.Progress()})
							texts = append(texts, prompt.Progress())
						}
					}
					cp.Messages[i].SystemSections = sections
					cp.Messages[i].Content[0].Text = strings.Join(texts, "\n\n")
				}

				if err := r.Checkpoint(ctx, claim, run.Version, cp); err != nil {
					t.Fatal(err)
				}
				if err := r.Release(ctx, claim); err != nil {
					t.Fatal(err)
				}
			}
			mustDrain(t, a)
			assertProgress(t, s, tc.want)
			if scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='child' AND status='completed'`) != 1 {
				t.Fatal("did not exercise an actual Child loop")
			}
		})
	}
}
