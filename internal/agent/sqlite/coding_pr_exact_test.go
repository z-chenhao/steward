package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/llm"
)

func codingGit(t *testing.T, source string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", source}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z"}
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual git %v: %v: %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func TestCodingCheckExactRemoteContentArtifactAndFinalFences(t *testing.T) {
	for _, scenario := range []string{"exact", "dirty", "source", "instruction", "watch", "forget", "unmanaged-coding", "unmanaged-artifact"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p, cfg := codingCheckFixture(t)
			switch scenario {
			case "unmanaged-coding": // Historical native report consumers still exist.
			case "unmanaged-artifact":
				installCodingPlugin(t, s, r)
			default:
				artifactCodingComposition(t, s, r)
			}
			codingGit(t, cfg.RepositorySource, "init")
			codingGit(t, cfg.RepositorySource, "add", ".")
			codingGit(t, cfg.RepositorySource, "commit", "-m", "owner fixture")
			head := codingGit(t, cfg.RepositorySource, "rev-parse", "HEAD")
			p.respond = delegateToolWork(func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{Text: "prepared"}, nil
			})
			mustSubmit(t, a, "exact-handoff", "prepare exact candidate")
			mustDrain(t, a)
			var ref, dir string
			if err := s.db.QueryRow(`SELECT t.task_ref,e.directory FROM task t JOIN task_code_environment e USING(task_id)`).Scan(&ref, &dir); err != nil {
				t.Fatal(err)
			}
			gh := githubConfig()
			poll := func() {
				t.Helper()
				ticket, err := s.BeginGitHubPoll(ctx, gh)
				if err != nil {
					t.Fatal(err)
				}
				batch := githubBatch()
				batch.HeadSHA = head
				batch.Events = nil
				if err = s.FinishGitHubPoll(ctx, gh, ticket, batch, ""); err != nil {
					t.Fatal(err)
				}
			}
			poll()
			bindTask(t, s, gh, ref)
			source := cfg.RepositorySource
			if scenario == "dirty" {
				if err := os.WriteFile(filepath.Join(dir, "extra-untracked"), []byte("not in PR"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "source" {
				source = filepath.Dir(source)
			}
			agentID := idAgent(t, s)
			reached := false
			s.MainBoundary = func(point string) {
				if point != "before_code_report_commit" {
					return
				}
				reached = true
				switch scenario {
				case "instruction":
					if _, err := s.db.Exec(`INSERT INTO task_instruction(instruction_id,task_id,task_epoch,instruction,artifact_refs,created_at) SELECT 'late-instruction',task_id,task_epoch,'new acceptance requirement','[]',? FROM task WHERE task_ref=?`, time.Now().UnixMicro(), ref); err != nil {
						t.Fatal(err)
					}
				case "watch":
					poll()
				case "forget":
					if err := r.write(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, agentID) }); err != nil {
						t.Fatal(err)
					}
				}
			}
			saved, err := s.SaveCodePRExactReport(ctx, ref, gh, head, source)
			if scenario == "exact" {
				if err != nil || !reached {
					t.Fatal("actual exact handoff failed", saved, err)
				}
				var report coding.Report
				if err = json.Unmarshal([]byte(readArtifact(t, s, artifactOwner(t, s), saved.Ref)), &report); err != nil {
					t.Fatal(err)
				}
				if report.PR == nil || report.PR.Kind != "owner_verified_exact_content" || report.PR.HeadSHA != head || report.PR.Content == nil || report.PR.Content.Head != head || report.PR.Content.TreeSHA256 != report.CandidateSHA256 || report.PR.Content.Driver == "" || report.Review == nil {
					t.Fatal("missing exact content proof", report)
				}
				if err = os.WriteFile(filepath.Join(dir, "file.txt"), []byte("later candidate"), 0600); err != nil {
					t.Fatal(err)
				}
				var retained coding.Report
				if err = json.Unmarshal([]byte(readArtifact(t, s, artifactOwner(t, s), saved.Ref)), &retained); err != nil || retained.CandidateSHA256 != report.CandidateSHA256 || retained.PR.Content.TreeSHA256 != report.PR.Content.TreeSHA256 {
					t.Fatal("handoff rebound", retained, err)
				}
				return
			}
			want := agent.ErrConflict
			if scenario == "unmanaged-coding" {
				want = llm.ProviderUnavailable
			}
			if scenario == "unmanaged-artifact" {
				want = artifact.ErrUnavailable
			}
			if scenario == "forget" {
				want = agent.ErrNotFound
			}
			if !errors.Is(err, want) || saved.Ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
				t.Fatal("invalid exact handoff accepted", scenario, saved, err)
			}
			if (scenario == "instruction" || scenario == "watch" || scenario == "forget") && !reached {
				t.Fatal("final commit fence not exercised")
			}
		})
	}
}
