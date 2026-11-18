package maintenance

import (
	"context"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const packagePrefix = "rsi/packages/"

func packagePath(name string) bool {
	tail, ok := strings.CutPrefix(name, packagePrefix)
	digest, file, split := strings.Cut(tail, "/")
	if !ok || !split || len(digest) != 64 || !fs.ValidPath(file) || file == "." || strings.ContainsAny(file, "\\\x00\r\n") {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil && strings.ToLower(digest) == digest
}

// Read the retained package inventory from Host state. Only its declared files
// can enter an archive. Detached package staging and secrets outside it stay private.
func packageFiles(ctx context.Context, root string, db *sql.DB) (map[string]os.FileMode, error) {
	files := map[string]os.FileMode{}
	rows, err := db.QueryContext(ctx, `SELECT digest,manifest_json FROM rsi_package p WHERE NOT EXISTS(SELECT 1 FROM rsi_retired r WHERE r.digest=p.digest) ORDER BY digest`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	catalog := rsi.NewCatalog(root)
	dirs := map[string]bool{".": true}
	for rows.Next() {
		var digest, raw string
		if err := rows.Scan(&digest, &raw); err != nil {
			return nil, err
		}
		var manifest rsiplugin.Manifest
		if rsiplugin.Decode([]byte(raw), &manifest) != nil || rsi.ValidateManifest(manifest) != nil {
			return nil, ErrInvalid
		}
		expected, err := rsiplugin.Digest("steward.rsi.package/1", manifest)
		if err != nil || expected != digest {
			return nil, ErrInvalid
		}
		base := packagePrefix + strings.TrimPrefix(digest, "sha256:") + "/"
		entries := append([]rsiplugin.File{{Path: "steward.plugin.json"}}, manifest.Files...)
		for _, entry := range entries {
			name := base + entry.Path
			if !packagePath(name) || len(files) >= maxBackupFiles {
				return nil, ErrInvalid
			}
			for dir := path.Dir(name); !dirs[dir]; dir = path.Dir(dir) {
				info, err := os.Lstat(filepath.Join(root, dir))
				if err != nil || !info.IsDir() {
					return nil, ErrInvalid
				}
				dirs[dir] = true
			}
			info, err := os.Lstat(filepath.Join(root, name))
			if err != nil || !privateRegular(info) {
				return nil, ErrInvalid
			}
			mode := os.FileMode(0400)
			if entry.Executable {
				mode = 0500
			}
			files[name] = mode
		}
		if _, err = catalog.Load(digest); err != nil {
			return nil, ErrInvalid
		}
	}
	return files, rows.Err()
}

// Legacy archives retain package digests in state.db. Missing retained packages
// can be reinstalled at their exact digest before the operator activates the root.
func checkActivationPackages(root string) error {
	ctx := context.Background()
	db, err := database(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return ErrInvalid
	}
	if version >= 52 {
		_, err = packageFiles(ctx, root, db)
	}
	return err
}

// Sync children before their parent before publishing the entire staging tree.
func syncRestoreDirectories(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, name)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}
