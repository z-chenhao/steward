package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
)

func TestArtifactWorkbenchOwnerForgetAfterStagingRejectsPublication(t *testing.T) {
	ctx := context.Background()
	s, runs, _ := artifactOwnerFixture(t)
	owner := artifactOwner(t, s)
	input, err := s.PrepareArtifactInput(ctx, artifact.Input{Name: "unpublished.txt", MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := s.Artifacts().Stage(ctx, input, strings.NewReader("private original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return runs.revokeMemory(ctx, tx, owner.AgentID) }); err != nil {
		t.Fatal(err)
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := s.Artifacts().CommitTx(ctx, tx, owner, stage, "")
		return err
	})
	if !errors.Is(err, artifact.ErrNotFound) || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
		t.Fatal("Forget accepted an old owner creation plan", err)
	}
}

func TestArtifactWorkbenchCleanupLateRevocationNeverDeletes(t *testing.T) {
	for _, cause := range []string{"forget", "stop", "quarantine"} {
		t.Run(cause, func(t *testing.T) {
			ctx := context.Background()
			s, runs, host := artifactOwnerFixture(t)
			owner := artifactOwner(t, s)
			ref := createOwnerArtifact(t, s, "eligible.txt", "text/plain", []byte("original bytes"))
			if _, err := s.db.Exec(`DROP TRIGGER artifact_immutable; UPDATE artifact SET created_at=1,expires_at=2`); err != nil {
				t.Fatal(err)
			}
			status, err := host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			s.MainBoundary = func(point string) {
				if point != "before_artifact_cleanup_commit" {
					return
				}
				switch cause {
				case "forget":
					err = s.Transaction(ctx, func(tx *sql.Tx) error { return runs.revokeMemory(ctx, tx, owner.AgentID) })
				case "stop":
					err = host.Store.Stop(ctx, true)
				case "quarantine":
					err = host.Store.Quarantine(ctx, status.Revision)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.CleanupArtifacts(ctx, false)
			want := error(artifact.ErrUnavailable)
			if cause == "forget" {
				want = agent.ErrClaimLost
			}
			if !errors.Is(err, want) || scalar(t, s, `SELECT count(*) FROM artifact WHERE artifact_ref=? AND deleted_at IS NULL`, ref) != 1 {
				t.Fatal("late revocation deleted an artifact", err)
			}
		})
	}
}
