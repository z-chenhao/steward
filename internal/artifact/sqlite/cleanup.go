package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/event"
	eventsqlite "github.com/z-chenhao/assistant/internal/event/sqlite"
)

func artifactEvent(ctx context.Context, tx *sql.Tx, ref, kind string) error {
	e, err := event.New("/steward/artifacts", "ai.steward.artifact."+kind+".v1", ref, map[string]string{"artifact_ref": ref})
	if err != nil {
		return err
	}
	return eventsqlite.Enqueue(ctx, tx, e)
}

// Recover removes only uncommitted objects and bytes behind tombstones. A DB
// write lock serializes metadata. File locks protect live staged objects while
// streaming outside SQLite, including in another process; os.Root confines unlink.
func (s *Store) Recover(ctx context.Context) (artifact.CleanupReport, error) {
	report := artifact.CleanupReport{Artifacts: []string{}}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		// Prove writeability under the same lock as writer/recovery. Never create
		// a startup probe outside it, where another process could collect the file.
		name := "tmp-" + token()
		f, e := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return artifact.ErrStorage
		}
		closeErr := f.Close()
		removeErr := s.root.Remove(name)
		if closeErr != nil || removeErr != nil {
			return artifact.ErrStorage
		}
		n, e := s.orphans(ctx, tx, false)
		report.Objects = n
		return e
	})
	return report, err
}
func (s *Store) orphans(ctx context.Context, tx *sql.Tx, dry bool) (int, error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return 0, artifact.ErrStorage
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return 0, artifact.ErrStorage
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !validToken(name) && !(strings.HasPrefix(name, "tmp-") && validToken(strings.TrimPrefix(name, "tmp-"))) {
			continue
		}
		var count int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM artifact WHERE storage_key=? AND deleted_at IS NULL`, name).Scan(&count) != nil {
			return n, artifact.ErrStorage
		}
		if count > 0 {
			continue
		}
		file, err := s.file(name)
		if err != nil {
			return n, err
		}
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			file.Close()
			continue
		}
		if err != nil {
			file.Close()
			return n, artifact.ErrStorage
		}
		if !dry {
			err = s.root.Remove(name)
		}
		file.Close()
		if err != nil {
			return n, artifact.ErrStorage
		}
		n++
	}
	if !dry && n > 0 {
		err = s.sync()
	}
	return n, err
}
