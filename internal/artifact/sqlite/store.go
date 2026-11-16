// Package sqlite stores metadata in state.db and immutable binary objects under
// an os.Root. The caller owns the DB and transactions shared with Tool results.
package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type Store struct {
	db     *sql.DB
	root   *os.Root
	Policy artifact.Policy // Configure once before accepting work.
	// Optional operator choices, configured once before accepting work. Nil
	// delegates to the managed recipe; these are never inferred from Policy.
	RetentionDays *int
	CleanupBatch  *int
	// Required by the product App. Bare native constructors are explicit SDK
	// fixtures/embedders; once a recipe activated, both paths remain managed.
	RequireManaged bool
	Boundary       func(string) // Explicit deterministic fault injection only.
}

func New(db *sql.DB, dataRoot string) (*Store, error) {
	if !filepath.IsAbs(dataRoot) {
		return nil, artifact.ErrInvalid
	}
	path := filepath.Join(dataRoot, "artifacts")
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, artifact.ErrStorage
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, artifact.ErrStorage
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, artifact.ErrStorage
	}
	return &Store{db: db, root: root, Policy: artifact.DefaultPolicy()}, nil
}
func (s *Store) Close() error { return s.root.Close() }
func (s *Store) boundary(name string) {
	if s.Boundary != nil {
		s.Boundary(name)
	}
}
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func validToken(v string) bool {
	b, err := hex.DecodeString(v)
	return len(v) == 64 && err == nil && len(b) == 32 && strings.ToLower(v) == v
}
func validRef(v string) bool {
	return strings.HasPrefix(v, "artifact:") && validToken(strings.TrimPrefix(v, "artifact:"))
}

func storedRef(id string) string {
	if !strings.Contains(id, ":") {
		return "artifact:" + id
	}
	return id
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact.ErrStorage
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return artifact.ErrStorage
	}
	return nil
}
func (s *Store) scope(ctx context.Context, tx *sql.Tx, a artifact.Scope) error {
	if a.UserID != artifact.LocalUser || a.AgentID == "" || (!a.Owner && a.RunID == "") {
		return artifact.ErrNotFound
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_config WHERE agent_id=?`, a.AgentID).Scan(&n); err != nil {
		return artifact.ErrStorage
	}
	if n != 1 {
		return artifact.ErrNotFound
	}
	if a.RunID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE run_id=? AND agent_id=? AND coalesce(task_id,'')=?`, a.RunID, a.AgentID, a.TaskID).Scan(&n); err != nil {
			return artifact.ErrStorage
		}
		if n != 1 {
			return artifact.ErrNotFound
		}
	} else if a.TaskID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task WHERE task_id=? AND agent_id=?`, a.TaskID, a.AgentID).Scan(&n); err != nil {
			return artifact.ErrStorage
		}
		if n != 1 {
			return artifact.ErrNotFound
		}
	}
	return nil
}
func (s *Store) DescribeTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, ref string, live bool) (artifact.Descriptor, error) {
	ref = storedRef(ref)
	var d artifact.Descriptor
	if !validRef(ref) {
		return d, artifact.ErrNotFound
	}
	if err := s.scope(ctx, tx, a); err != nil {
		return d, err
	}
	var user, agentID, taskID, runID, visibility string
	var created, expires int64
	var deleted sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT artifact_ref,name,media_type,byte_size,content_hash,created_at,expires_at,deleted_at,version,owner_user_id,owner_agent_id,coalesce(task_id,''),coalesce(run_id,''),visibility,coalesce(method_graph,''),coalesce(semantic_kind,'') FROM artifact WHERE artifact_ref=?`, ref).Scan(&d.Ref, &d.Name, &d.MediaType, &d.ByteSize, &d.ContentHash, &created, &expires, &deleted, &d.Version, &user, &agentID, &taskID, &runID, &visibility, &d.MethodGraph, &d.SemanticKind)
	if err == sql.ErrNoRows {
		return d, artifact.ErrNotFound
	}
	if err != nil {
		return d, artifact.ErrStorage
	}
	if user != a.UserID || agentID != a.AgentID {
		return artifact.Descriptor{}, artifact.ErrNotFound
	}
	visible := a.Owner || (visibility == "task" && taskID != "" && taskID == a.TaskID) || (visibility == "run" && runID == a.RunID)
	if !visible {
		var n int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM artifact_reference WHERE artifact_ref=? AND ((holder_kind='task' AND holder_id=?) OR (holder_kind='run' AND holder_id=?))`, ref, a.TaskID, a.RunID).Scan(&n)
		if err != nil {
			return d, artifact.ErrStorage
		}
		visible = n > 0
	}
	// Task-owned and explicitly attached inputs remain recoverable for the
	// complete Task lifecycle, then retain their creation-time TTL after it ends.
	protected, effective, err := s.expiry(ctx, tx, ref, taskID, created, expires)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	expires = effective
	if !visible || ((live || !a.Owner) && (deleted.Valid || (!protected && expires <= time.Now().UnixMicro()))) {
		return artifact.Descriptor{}, artifact.ErrNotFound
	}
	d.CreatedAt = time.UnixMicro(created).UTC()
	if !protected {
		at := time.UnixMicro(expires).UTC()
		d.ExpiresAt = &at
	}
	if deleted.Valid {
		v := time.UnixMicro(deleted.Int64).UTC()
		d.DeletedAt = &v
	}
	return d, nil
}
func (s *Store) expiry(ctx context.Context, tx *sql.Tx, ref, taskID string, created, expires int64) (bool, int64, error) {
	var active int
	var ended int64
	err := tx.QueryRowContext(ctx, `SELECT coalesce(max(status IN ('active','paused')),0),coalesce(max(coalesce(completed_at,cancelled_at,created_at)),0) FROM task t WHERE t.task_id=? OR EXISTS(SELECT 1 FROM artifact_reference r WHERE r.artifact_ref=? AND r.holder_kind='task' AND r.holder_id=t.task_id)`, taskID, ref).Scan(&active, &ended)
	if err != nil {
		return false, 0, artifact.ErrStorage
	}
	if ended > 0 && ended+expires-created > expires {
		expires = ended + expires - created
	}
	return active > 0, expires, nil
}

func (s *Store) Describe(ctx context.Context, a artifact.Scope, ref string) (d artifact.Descriptor, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error { var e error; d, e = s.DescribeTx(ctx, tx, a, ref, false); return e })
	return
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(b)
	if cause := r.ctx.Err(); cause != nil {
		return 0, cause
	}
	return n, err
}
func (s *Store) sync() error {
	f, e := s.root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (s *Store) file(key string) (*os.File, error) {
	if !validToken(key) && !(strings.HasPrefix(key, "tmp-") && validToken(strings.TrimPrefix(key, "tmp-"))) {
		return nil, artifact.ErrIntegrity
	}
	info, err := s.root.Lstat(key)
	if err != nil || !info.Mode().IsRegular() {
		return nil, artifact.ErrIntegrity
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, artifact.ErrIntegrity
	}
	return f, nil
}
func (s *Store) verify(key string, n int64, sum string) error {
	f, err := s.file(key)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != n {
		return artifact.ErrIntegrity
	}
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, n+1)); err != nil || hex.EncodeToString(h.Sum(nil)) != sum {
		return artifact.ErrIntegrity
	}
	return nil
}

// Opened binds authorization metadata to the exact file descriptor. It cannot
// be consumed until Verify has checked the complete object outside SQLite.
type Opened struct {
	file       *os.File
	descriptor artifact.Descriptor
}

func (o *Opened) Descriptor() artifact.Descriptor { return o.descriptor }
func (o *Opened) Close() error                    { return o.file.Close() }
func (o *Opened) Verify(ctx context.Context) (io.ReadCloser, error) {
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(&contextReader{ctx, o.file}, o.descriptor.ByteSize+1))
	if err != nil || n != o.descriptor.ByteSize || hex.EncodeToString(hash.Sum(nil)) != o.descriptor.ContentHash {
		o.file.Close()
		return nil, artifact.ErrIntegrity
	}
	if _, err = o.file.Seek(0, io.SeekStart); err != nil {
		o.file.Close()
		return nil, artifact.ErrStorage
	}
	return o.file, nil
}
func (s *Store) OpenFileTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, ref string) (*Opened, error) {
	ref = storedRef(ref)
	descriptor, err := s.DescribeTx(ctx, tx, a, ref, true)
	if err != nil {
		return nil, err
	}
	var key string
	if tx.QueryRowContext(ctx, `SELECT storage_key FROM artifact WHERE artifact_ref=?`, ref).Scan(&key) != nil {
		return nil, artifact.ErrStorage
	}
	file, err := s.file(key)
	if err != nil {
		return nil, err
	}
	return &Opened{file: file, descriptor: descriptor}, nil
}
func (s *Store) Open(ctx context.Context, a artifact.Scope, ref string) (io.ReadCloser, error) {
	var opened *Opened
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var e error
		opened, e = s.OpenFileTx(ctx, tx, a, ref)
		return e
	})
	if err != nil {
		if opened != nil {
			opened.Close()
		}
		return nil, err
	}
	return opened.Verify(ctx)
}

func (s *Store) PinTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, ref, kind, id string) error {
	ref = storedRef(ref)
	if _, err := s.DescribeTx(ctx, tx, a, ref, true); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO artifact_reference VALUES(?,?,?) ON CONFLICT DO NOTHING`, ref, kind, id)
	return err
}

// PublishTx revalidates a preverified complete set before any mutation.
// Expected validation failures leave the transaction clean.
func (s *Store) PublishTx(ctx context.Context, tx *sql.Tx, a artifact.Scope, publication *Publication) error {
	if publication == nil || publication.store != s || publication.scope != a {
		return artifact.ErrInvalid
	}
	refs := publication.refs
	if a.RunID == "" {
		return artifact.ErrInvalid
	}
	if len(refs) > artifact.MaxPublished {
		return artifact.ErrLimit
	}
	if err := s.scope(ctx, tx, a); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT artifact_ref FROM artifact_publication WHERE run_id=?`, a.RunID)
	if err != nil {
		return artifact.ErrStorage
	}
	selected := map[string]bool{}
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			rows.Close()
			return artifact.ErrStorage
		}
		selected[ref] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return artifact.ErrStorage
	}
	for _, ref := range refs {
		selected[ref] = true
	}
	if len(selected) > artifact.MaxPublished {
		return artifact.ErrLimit
	}
	for _, ref := range refs {
		descriptor, err := s.DescribeTx(ctx, tx, a, ref, true)
		if err != nil {
			return err
		}
		verified, ok := publication.verified[ref]
		if !ok || descriptor.ContentHash != verified.ContentHash || descriptor.Version != verified.Version {
			return artifact.ErrIntegrity
		}
	}

	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_reference VALUES(?,'publication',?) ON CONFLICT DO NOTHING`, ref, a.RunID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO artifact_publication VALUES(?,?) ON CONFLICT DO NOTHING`, a.RunID, ref)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if err = artifactEvent(ctx, tx, ref, "published"); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) Delete(ctx context.Context, a artifact.Scope, ref string, version int64) error {
	ref = storedRef(ref)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		d, err := s.DescribeTx(ctx, tx, a, ref, false)
		if err != nil {
			return err
		}
		if !a.Owner {
			return artifact.ErrNotFound
		}
		if d.Version != version {
			return artifact.ErrConflict
		}
		if d.DeletedAt != nil {
			return nil
		}
		var n int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM artifact_reference WHERE artifact_ref=?`, ref).Scan(&n) != nil {
			return artifact.ErrStorage
		}
		if n > 0 {
			return artifact.ErrReferenced
		}
		return s.tombstone(ctx, tx, ref, version)
	})
}
func (s *Store) tombstone(ctx context.Context, tx *sql.Tx, ref string, version int64) error {
	res, err := tx.ExecContext(ctx, `UPDATE artifact SET deleted_at=?,version=version+1 WHERE artifact_ref=? AND version=? AND deleted_at IS NULL`, time.Now().UnixMicro(), ref, version)
	if err != nil {
		return artifact.ErrStorage
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return artifact.ErrConflict
	}
	return artifactEvent(ctx, tx, ref, "deleted")
}

// Publication proves that all selected bytes were checked before the write
// transaction. Commit revalidates scope, liveness and metadata as one set.
type Publication struct {
	store    *Store
	scope    artifact.Scope
	refs     []string
	verified map[string]artifact.Descriptor
}

func (s *Store) PreparePublication(ctx context.Context, scope artifact.Scope, refs []string) (*Publication, error) {
	refs = append([]string{}, refs...)
	for i, ref := range refs {
		refs[i] = storedRef(ref)
	}
	if scope.RunID == "" {
		return nil, artifact.ErrInvalid
	}
	if len(refs) > artifact.MaxPublished {
		return nil, artifact.ErrLimit
	}
	publication := &Publication{store: s, scope: scope, refs: append([]string{}, refs...), verified: make(map[string]artifact.Descriptor)}
	for _, ref := range refs {
		descriptor, err := s.Describe(ctx, scope, ref)
		if err != nil {
			return nil, err
		}
		file, err := s.Open(ctx, scope, ref)
		if err != nil {
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, artifact.ErrStorage
		}
		publication.verified[ref] = descriptor
	}
	return publication, nil
}
