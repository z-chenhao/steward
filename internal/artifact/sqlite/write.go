package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/artifact"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"time"
)

func (s *Store) Put(ctx context.Context, a artifact.Scope, in artifact.Input, src io.Reader) (ref string, err error) {
	staged, err := s.Stage(ctx, in, src)
	if err != nil {
		return "", err
	}
	defer staged.Close()
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var e error
		ref, e = s.CommitTx(ctx, tx, a, staged, "")
		return e
	})
	return
}

// Staged owns an immutable, verified object and its cross-process file lock.
// Close is mandatory even after a transaction fails. Uncommitted published
// objects remain recoverable; a process death releases the lock automatically.
type Staged struct {
	store     *Store
	file      *os.File
	object    string
	input     artifact.Input
	size      int64
	sum       string
	published bool
}

func (st *Staged) Close() {
	if st == nil || st.file == nil {
		return
	}
	// Unlink while still owning the lock; recovery cannot race this owner.
	if !st.published {
		_ = st.store.root.Remove("tmp-" + st.object)
	}
	_ = st.file.Close()
	st.file = nil
}

// Stage consumes no state transaction while reading, syncing or hashing bytes.
// The short creation transaction closes the create/lock/recovery race.
func (s *Store) Stage(ctx context.Context, in artifact.Input, src io.Reader) (_ *Staged, err error) {
	if src == nil || in.Validate() != nil || s.Policy.Validate() != nil {
		return nil, artifact.ErrInvalid
	}
	if in.Creation != nil {
		if !in.Creation.Valid() {
			return nil, artifact.ErrInvalid
		}
		copy := *in.Creation
		copy.MediaKinds = append([]string{}, copy.MediaKinds...)
		in.Creation = &copy
	}
	st := &Staged{store: s, object: token(), input: in}
	err = s.transaction(ctx, func(*sql.Tx) error {
		var e error
		st.file, e = s.root.OpenFile("tmp-"+st.object, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return artifact.ErrStorage
		}
		if unix.Flock(int(st.file.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
			return artifact.ErrStorage
		}
		return nil
	})
	defer func() {
		if err != nil {
			st.Close()
		}
	}()
	if err != nil {
		return nil, err
	}
	s.boundary("artifact_temp_created")
	hash := sha256.New()
	st.size, err = io.Copy(io.MultiWriter(st.file, hash), io.LimitReader(&contextReader{ctx, src}, s.Policy.MaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, artifact.ErrStorage
	}
	if st.size > s.Policy.MaxBytes {
		return nil, artifact.ErrLimit
	}
	st.sum = hex.EncodeToString(hash.Sum(nil))
	if st.file.Chmod(0400) != nil || st.file.Sync() != nil {
		return nil, artifact.ErrStorage
	}
	if err = s.verify("tmp-"+st.object, st.size, st.sum); err != nil {
		return nil, err
	}
	return st, nil
}

// CommitTx admits quota and publishes metadata atomically with the owning Tool
// result. Scope and fencing remain the caller's transaction responsibility.
// key is a Host-owned invocation key; models and users never provide it.
func (s *Store) CommitTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, st *Staged, key string) (string, error) {
	if st == nil || st.store != s || st.file == nil {
		return "", artifact.ErrInvalid
	}
	if err := s.scope(ctx, tx, a); err != nil {
		return "", err
	}
	if st.input.RunOnly && a.RunID == "" {
		return "", artifact.ErrInvalid
	}
	var err error
	if key != "" {
		var ref, oldHash, name, media, runID, visibility string
		err = tx.QueryRowContext(ctx, `SELECT artifact_ref,content_hash,name,media_type,coalesce(run_id,''),visibility FROM artifact WHERE put_key=?`, key).Scan(&ref, &oldHash, &name, &media, &runID, &visibility)
		if err == nil {
			if oldHash != st.sum || name != st.input.Name || media != st.input.MediaType || runID != a.RunID || st.input.RunOnly && visibility != "run" {
				return "", artifact.ErrConflict
			}
			_, err = s.DescribeTx(ctx, tx, a, ref, true)
			return ref, err
		}
		if err != sql.ErrNoRows {
			return "", artifact.ErrStorage
		}
	}
	creation, err := s.creationTx(ctx, tx, a, st.input)
	if err != nil {
		return "", err
	}
	var used int64
	if tx.QueryRowContext(ctx, `SELECT coalesce(sum(byte_size),0) FROM artifact WHERE owner_user_id=? AND deleted_at IS NULL`, a.UserID).Scan(&used) != nil {
		return "", artifact.ErrStorage
	}
	if st.size > s.Policy.QuotaBytes-used {
		return "", artifact.ErrLimit
	}

	s.boundary("artifact_before_publish")
	if !st.published {
		if s.root.Rename("tmp-"+st.object, st.object) != nil || s.sync() != nil {
			return "", artifact.ErrStorage
		}
		st.published = true
	}
	s.boundary("artifact_after_publish")
	ref := "artifact:" + token()
	id := uuid.Must(uuid.NewV7()).String()
	now := time.Now().UTC()
	visibility := "agent"
	if st.input.RunOnly {
		visibility = "run"
	} else if a.TaskID != "" {
		visibility = "task"
	} else if a.RunID != "" {
		visibility = "run"
	}
	var putKey any
	if key != "" {
		putKey = key
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artifact(artifact_id,artifact_ref,owner_user_id,owner_agent_id,task_id,run_id,media_type,name,byte_size,content_hash,storage_key,visibility,retention_class,created_at,expires_at,put_key,method_graph,semantic_kind) VALUES(?,?,?,?,nullif(?,''),nullif(?,''),?,?,?,?,?,?,'standard',?,?,?,nullif(?,''),nullif(?,''))`, id, ref, a.UserID, a.AgentID, a.TaskID, a.RunID, st.input.MediaType, st.input.Name, st.size, st.sum, st.object, visibility, now.UnixMicro(), now.Add(time.Duration(creation.days)*24*time.Hour).UnixMicro(), putKey, creation.graph, creation.kind)
	if err != nil {
		return "", artifact.ErrStorage
	}
	if err = artifactEvent(ctx, tx, ref, "created"); err != nil {
		return "", err
	}
	s.boundary("artifact_after_metadata")
	return ref, nil
}
