package maintenance

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
)

const maxBackupBytes = int64(64 << 30)
const maxBackupFiles = 65536

type cancellingReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancellingReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func privateRegular(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode().IsRegular() && ok && stat.Nlink == 1
}

type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version   int            `json:"version"`
	CreatedAt string         `json:"created_at"`
	Schemas   map[string]int `json:"schemas"`
	Files     []File         `json:"files"`
}

func database(ctx context.Context, path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, ErrInvalid
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Preflight every DB before migrating any DB. Future schemas fail closed even
// when a different database still needs a supported forward migration.
func CheckVersions(ctx context.Context, root string, max map[string]int) error {
	for name, limit := range max {
		path := filepath.Join(root, name)
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !privateRegular(info) {
			return ErrInvalid
		}
		db, e := database(ctx, path)
		if e != nil {
			return e
		}
		var version int
		e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
		db.Close()
		if e != nil {
			return ErrInvalid
		}
		if version < 0 || version > limit {
			return fmt.Errorf("%w: unsupported database schema version %d (maximum %d; database %s)", ErrInvalid, version, limit, name)
		}
	}
	return nil
}

func allowed(path string) bool {
	return path == "state.db" || path == "conversation.db" || path == "memory.db" || path == "default-memory.json" || strings.HasPrefix(path, "artifacts/") && !strings.Contains(strings.TrimPrefix(path, "artifacts/"), "/") && len(strings.TrimPrefix(path, "artifacts/")) > 0
}
func inspect(ctx context.Context, root string, max map[string]int, checkpoint bool, archiveVersion int) (Manifest, error) {
	m := Manifest{Version: archiveVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Schemas: map[string]int{}}
	if CheckVersions(ctx, root, max) != nil {
		return m, ErrInvalid
	}
	for _, name := range []string{"state.db", "conversation.db", "memory.db"} {
		db, e := database(ctx, filepath.Join(root, name))
		if e != nil {
			return m, e
		}
		e = func() error {
			if checkpoint {
				var busy, log, done int
				if db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done) != nil || busy != 0 {
					return ErrBusy
				}
			}
			var integrity string
			if db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
				return ErrInvalid
			}
			rows, e := db.QueryContext(ctx, "PRAGMA foreign_key_check")
			if e != nil {
				return ErrInvalid
			}
			bad := rows.Next()
			e = rows.Err()
			rows.Close()
			if bad || e != nil {
				return ErrInvalid
			}
			var v int
			if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v) != nil {
				return ErrInvalid
			}
			limit, ok := max[name]
			if !ok || v < 1 || v > limit {
				return ErrInvalid
			}
			m.Schemas[name] = v
			return nil
		}()
		db.Close()
		if e != nil {
			return m, e
		}
	}
	paths := []string{"state.db", "conversation.db", "memory.db"}
	if _, err := os.Lstat(filepath.Join(root, "default-memory.json")); err == nil {
		paths = append(paths, "default-memory.json")
	} else if !os.IsNotExist(err) {
		return m, ErrInvalid
	}
	info, e := os.Lstat(filepath.Join(root, "artifacts"))
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return m, ErrInvalid
	}
	entries, e := os.ReadDir(filepath.Join(root, "artifacts"))
	if e != nil {
		return m, ErrInvalid
	}
	for _, entry := range entries {
		paths = append(paths, "artifacts/"+entry.Name())
	}
	if archiveVersion >= 2 && m.Schemas["state.db"] >= 52 {
		db, err := database(ctx, filepath.Join(root, "state.db"))
		if err != nil {
			return m, err
		}
		packages, err := packageFiles(ctx, root, db)
		db.Close()
		if err != nil {
			return m, err
		}
		for name, mode := range packages {
			paths = append(paths, name)
			if !checkpoint {
				if err = os.Chmod(filepath.Join(root, name), mode); err != nil {
					return m, err
				}
			}
		}
	}
	sort.Strings(paths)
	if len(paths) > maxBackupFiles {
		return m, ErrInvalid
	}
	var total int64
	for _, path := range paths {
		if ctx.Err() != nil {
			return m, ctx.Err()
		}
		info, e := os.Lstat(filepath.Join(root, path))
		if e != nil || !privateRegular(info) {
			return m, ErrInvalid
		}
		if info.Size() > maxBackupBytes-total {
			return m, ErrInvalid
		}
		total += info.Size()
		f, e := os.Open(filepath.Join(root, path))
		if e != nil {
			return m, ErrInvalid
		}
		h := sha256.New()
		n, e := io.Copy(h, cancellingReader{ctx, f})
		f.Close()
		if e != nil {
			return m, e
		}
		m.Files = append(m.Files, File{path, n, hex.EncodeToString(h.Sum(nil))})
	}
	// Verify the immutable bytes against authoritative live Artifact metadata.
	db, e := database(ctx, filepath.Join(root, "state.db"))
	if e != nil {
		return m, e
	}
	defer db.Close()
	if m.Schemas["state.db"] >= 11 {
		rows, e := db.QueryContext(ctx, "SELECT storage_key,byte_size,content_hash FROM artifact WHERE deleted_at IS NULL")
		if e != nil {
			return m, ErrInvalid
		}
		defer rows.Close()
		files := map[string]File{}
		for _, v := range m.Files {
			files[v.Path] = v
		}
		for rows.Next() {
			var key, hash string
			var n int64
			if rows.Scan(&key, &n, &hash) != nil {
				return m, ErrInvalid
			}
			v, ok := files["artifacts/"+key]
			if !ok || v.Bytes != n || v.SHA256 != hash {
				return m, ErrInvalid
			}
		}
		if rows.Err() != nil {
			return m, ErrInvalid
		}
	}
	return m, nil
}

func Backup(ctx context.Context, root, target string, key []byte, max map[string]int) (Manifest, error) {
	var empty Manifest
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return empty, ErrInvalid
	}
	realParent, e := filepath.EvalSymlinks(filepath.Dir(target))
	if e != nil {
		return empty, ErrInvalid
	}
	if !filepath.IsAbs(target) || within(realRoot, filepath.Join(realParent, filepath.Base(target))) {
		return empty, ErrInvalid
	}
	lock, e := Lock(root, true)
	if e != nil {
		return empty, e
	}
	defer lock.Close()
	m, e := inspect(ctx, root, max, true, 2)
	if e != nil {
		return empty, e
	}
	f, e := os.CreateTemp(filepath.Dir(target), ".steward-backup-*")
	if e != nil {
		return empty, ErrInvalid
	}
	defer os.Remove(f.Name())
	defer f.Close()
	w, e := encrypt(f, key)
	if e != nil {
		return empty, e
	}
	buffer := bufio.NewWriterSize(w, chunk)
	archive := tar.NewWriter(buffer)
	for _, entry := range m.Files {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		if e = archive.WriteHeader(&tar.Header{Name: entry.Path, Mode: 0600, Size: entry.Bytes, Typeflag: tar.TypeReg}); e != nil {
			return empty, e
		}
		source, e := os.Open(filepath.Join(root, entry.Path))
		if e != nil {
			return empty, e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(archive, h), cancellingReader{ctx, source})
		source.Close()
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		if e != nil || n != entry.Bytes || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return empty, ErrInvalid
		}
	}
	body, e := json.Marshal(m)
	if e != nil || len(body) > 8<<20 {
		return empty, ErrInvalid
	}
	if e = archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); e != nil {
		return empty, e
	}
	if _, e = archive.Write(body); e != nil {
		return empty, e
	}
	if archive.Close() != nil || buffer.Flush() != nil || w.Close() != nil || f.Sync() != nil || f.Close() != nil {
		return empty, ErrInvalid
	}
	// Link publishes atomically without replacing an existing archive.
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if os.Link(f.Name(), target) != nil {
		return empty, ErrInvalid
	}
	if syncDir(filepath.Dir(target)) != nil {
		return empty, ErrInvalid
	}
	return m, nil
}
func within(root, path string) bool {
	rel, e := filepath.Rel(root, path)
	return e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Restore publishes only a new root. Existing roots are never overwritten.
// The caller must retire the old instance before resuming the restored copy.
func Restore(ctx context.Context, source, root string, key []byte, max map[string]int) (Manifest, error) {
	var empty Manifest
	if !filepath.IsAbs(root) || !filepath.IsAbs(source) {
		return empty, ErrInvalid
	}
	info, e := os.Lstat(source)
	if e != nil || !info.Mode().IsRegular() || info.Size() > maxBackupBytes+256<<20 {
		return empty, ErrInvalid
	}
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		return empty, ErrInvalid
	}
	f, e := os.Open(source)
	if e != nil {
		return empty, ErrInvalid
	}
	defer f.Close()
	reader, e := decrypt(f, key)
	if e != nil {
		return empty, e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(root), ".steward-restore-*")
	if e != nil {
		return empty, ErrInvalid
	}
	defer os.RemoveAll(tmp)
	if os.Mkdir(filepath.Join(tmp, "artifacts"), 0700) != nil {
		return empty, ErrInvalid
	}
	archive := tar.NewReader(reader)
	seen := map[string]bool{}
	var m Manifest
	manifestSeen := false
	var total int64
	for {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return empty, ErrInvalid
		}
		name := header.Name
		if manifestSeen || seen[name] || header.Typeflag != tar.TypeReg || header.Size < 0 || name != filepath.ToSlash(filepath.Clean(name)) || name == ".." || strings.Contains(name, "\\") || name != "manifest.json" && !allowed(name) && !packagePath(name) {
			return empty, ErrInvalid
		}
		seen[name] = true
		if name == "manifest.json" {
			if header.Size > 8<<20 {
				return empty, ErrInvalid
			}
			decoder := json.NewDecoder(io.LimitReader(archive, header.Size))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&m) != nil || (m.Version != 1 && m.Version != 2) {
				return empty, ErrInvalid
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return empty, ErrInvalid
			}
			created, e := time.Parse(time.RFC3339Nano, m.CreatedAt)
			if e != nil {
				return empty, ErrInvalid
			}
			_, offset := created.Zone()
			if offset != 0 {
				return empty, ErrInvalid
			}
			manifestSeen = true
			continue
		}
		if header.Size > maxBackupBytes-total || len(seen) > maxBackupFiles+1 {
			return empty, ErrInvalid
		}
		total += header.Size
		if packagePath(name) {
			if e = os.MkdirAll(filepath.Dir(filepath.Join(tmp, name)), 0700); e != nil {
				return empty, e
			}
		}
		destination, e := os.OpenFile(filepath.Join(tmp, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return empty, ErrInvalid
		}
		n, e := io.Copy(destination, cancellingReader{ctx, archive})
		syncErr := destination.Sync()
		closeErr := destination.Close()
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		if e != nil || syncErr != nil || closeErr != nil || n != header.Size {
			return empty, ErrInvalid
		}
	}
	// tar EOF precedes the authenticated archive terminator; consume it too.
	padding, e := io.ReadAll(io.LimitReader(reader, chunk+1))
	if e != nil || len(padding) > chunk || !reader.final {
		return empty, ErrInvalid
	}
	for _, v := range padding {
		if v != 0 {
			return empty, ErrInvalid
		}
	}
	if !manifestSeen {
		return empty, ErrInvalid
	}
	actual, e := inspect(ctx, tmp, max, false, m.Version)
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if e != nil || len(seen) != len(actual.Files)+1 || !reflect.DeepEqual(actual.Schemas, m.Schemas) || !reflect.DeepEqual(actual.Files, m.Files) {
		return empty, ErrInvalid
	}
	// Keep the operator's recovery point policy explicit; never silently resume
	// a second copy of an identity that can send external effects.
	if os.WriteFile(filepath.Join(tmp, ".restore-pending"), []byte("retire source and reconcile external outcomes before ops activate\n"), 0600) != nil {
		return empty, ErrInvalid
	}
	if err := syncRestoreDirectories(tmp); err != nil {
		return empty, err
	}
	// A parent lock serializes restores targeting the same new root.
	parentLock, e := Lock(filepath.Dir(root), true)
	if e != nil {
		return empty, e
	}
	defer parentLock.Close()
	if _, e = os.Lstat(root); !os.IsNotExist(e) {
		return empty, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if os.Rename(tmp, root) != nil || syncDir(filepath.Dir(root)) != nil {
		return empty, ErrInvalid
	}
	return m, nil
}

func Pending(root string) bool {
	_, e := os.Lstat(filepath.Join(root, ".restore-pending"))
	return !os.IsNotExist(e)
}

// Activate is an explicit operator attestation, never an automatic restore step.
func Activate(root, attestation string) error {
	if attestation != "source-retired-and-outcomes-reconciled" {
		return ErrInvalid
	}
	lock, e := Lock(root, true)
	if e != nil {
		return e
	}
	defer lock.Close()
	if err := checkActivationPackages(root); err != nil {
		return err
	}
	if os.Remove(filepath.Join(root, ".restore-pending")) != nil {
		return ErrInvalid
	}
	return syncDir(root)
}
