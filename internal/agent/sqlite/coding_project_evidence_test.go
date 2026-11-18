package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

func projectEvidenceDocument(paths []string) contract.ProjectDocument {
	step := func(command string) contract.ProjectStep {
		return contract.ProjectStep{Command: command, TimeoutSeconds: 5}
	}
	return contract.ProjectDocument{Version: 1, Name: "evidence-fixture", Prepare: step(`mkdir -p .evidence`), Doctor: step(`test -d .evidence`), Cleanup: step(`true`), Checks: []contract.ProjectCheck{{Name: "state", Requirement: "The independently captured files remain reviewable after environment removal", Evidence: paths, ProjectStep: step(`printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2ioAAAAASUVORK5CYII=' | base64 -d > .evidence/frame.png; printf '{"actual":"retained"}' > .evidence/assertions.json`)}}}
}

func projectEvidenceReceipt(t *testing.T, s *Store) (tool.Result, coding.ProjectReceipt) {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name='builtin__coding_verify'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var receipt coding.ProjectReceipt
	if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &receipt) != nil {
		t.Fatal("missing original receipt", raw)
	}
	return result, receipt
}

func TestCodingProjectEvidenceUsesManagedAtomicArtifactsAfterContainerRemoval(t *testing.T) {
	for _, cell := range []bool{false, true} {
		t.Run(fmt.Sprint("code-mode-", cell), func(t *testing.T) {
			doc := projectEvidenceDocument([]string{".evidence/frame.png", ".evidence/assertions.json"})
			s, r, a, _ := queuedCodingProjectDocument(t, &doc, cell)
			approveCodingCheck(t, s, r)
			mustDrain(t, a)
			result, receipt := projectEvidenceReceipt(t, s)
			if receipt.Outcome != "passed" || len(receipt.Evidence) != 2 || len(receipt.EvidenceFailures) != 0 || len(result.ArtifactRefs) != 2 || len(result.ImageRefs) != 1 {
				t.Fatal(result, receipt)
			}
			var directory, graph string
			if err := s.db.QueryRow(`SELECT x.directory,p.graph_revision FROM coding_project_resource x JOIN coding_project_plan p USING(invocation_id) WHERE x.released=1`).Scan(&directory, &graph); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("candidate persisted instead of its scoped evidence", err)
			}
			for i, item := range receipt.Evidence {
				if item.ArtifactIndex != i || item.Path != doc.Checks[0].Evidence[i] {
					t.Fatal("declared path association changed", item)
				}
				data := readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[i])
				if item.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(data))) || item.Bytes != int64(len(data)) {
					t.Fatal("captured bytes changed after removal", item)
				}
				meta, err := s.Artifacts().Describe(context.Background(), artifactOwner(t, s), result.ArtifactRefs[i])
				if err != nil || meta.MethodGraph != graph || meta.ContentHash != item.SHA256 {
					t.Fatal("evidence bypassed original managed Artifact recipe", meta, err)
				}
			}
			if readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[1]) != `{"actual":"retained"}` {
				t.Fatal("structured evidence unavailable")
			}
			mustDrain(t, a)
			if scalar(t, s, `SELECT count(*) FROM artifact`) != 2 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND attempt_count=1`) != 1 {
				t.Fatal("evidence replayed instead of reopening original refs")
			}
		})
	}
}

func TestCodingProjectMissingEvidenceDoesNotTurnZeroExitIntoAcceptance(t *testing.T) {
	doc := projectEvidenceDocument([]string{".evidence/missing.png"})
	s, r, a, _ := queuedCodingProjectDocument(t, &doc)
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	result, receipt := projectEvidenceReceipt(t, s)
	if receipt.Outcome != "incomplete" || len(receipt.EvidenceFailures) != 1 || receipt.EvidenceFailures[0].ErrorCode != "NOT_FOUND" || len(result.ArtifactRefs) != 0 {
		t.Fatal(result, receipt)
	}
	for _, phase := range receipt.Phases {
		if phase.ExitCode == nil || *phase.ExitCode != 0 || phase.Status != "passed" {
			t.Fatal("original phase facts were rewritten", phase)
		}
	}
}

func TestCodingProjectEvidenceTerminalFailureRollsBackRefsWithoutReexecution(t *testing.T) {
	doc := projectEvidenceDocument([]string{".evidence/assertions.json"})
	s, r, a, _ := queuedCodingProjectDocument(t, &doc)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_evidence_terminal BEFORE UPDATE ON tool_invocation WHEN OLD.tool_name='builtin__coding_verify' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	approveCodingCheck(t, s, r)
	if err := a.Drain(context.Background()); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("actual atomic receipt failure not reached", err)
	}
	if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 {
		t.Fatal("partial refs or unresolved physical release")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_evidence_terminal`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='unknown' AND attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 1 {
		t.Fatal("storage recovery recreated verification evidence")
	}
}

func TestCodingProjectEvidenceLateForgetReleasesResourcesWithoutPublishing(t *testing.T) {
	doc := projectEvidenceDocument([]string{".evidence/frame.png"})
	s, r, _, _ := queuedCodingProjectDocument(t, &doc)
	ctx := context.Background()
	approveCodingCheck(t, s, r)
	var run, invocation string
	if err := s.db.QueryRow(`SELECT run_id,invocation_id FROM tool_invocation WHERE tool_name='builtin__coding_verify'`).Scan(&run, &invocation); err != nil {
		t.Fatal(err)
	}
	claim, err := r.Claim(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	store := r.ToolStore(claim)
	if err := store.Start(ctx, invocation); err != nil {
		t.Fatal(err)
	}
	hit := false
	owner := idAgent(t, s)
	s.MainBoundary = func(point string) {
		if point != "before_coding_project_evidence" {
			return
		}
		hit = true
		if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.(tool.HostStore).ExecuteBuiltin(ctx, tool.Invocation{ID: invocation})
	if !hit || !errors.Is(err, agent.ErrClaimLost) || result.Text != "" || len(result.Attachments) != 0 || len(result.ArtifactRefs) != 0 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
		t.Fatal("revoked evidence escaped its original scope", hit, result, err)
	}
	if err := r.recoverProjectResources(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 1 {
		t.Fatal("privacy revocation lost known physical cleanup or immutable history")
	}
}
