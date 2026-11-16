// Package coding prepares private Task working copies. Source Git metadata is
// never imported or executed; the file-tree snapshot includes owner dirty files.
package coding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"golang.org/x/sys/unix"
)

var ErrInvalid = errors.New("code workspace unavailable or unsafe")

const maxFiles = 4096
const maxBytes = 64 << 20

type Workspace struct {
	TaskRef      string `json:"task_ref"`
	Directory    string `json:"directory,omitempty"`
	Baseline     string `json:"-"`
	BaselineHash string `json:"baseline_sha256"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
}
type receipt struct {
	Version      int    `json:"version"`
	Scope        string `json:"scope"`
	BaselineHash string `json:"baseline_sha256"`
}
type manifestFile struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	Mode uint32 `json:"mode"`
}

// scanTree validates every entry through an anchored os.Root. No
// symlink/hardlink/device is accepted, including after work.
func scanTree(ctx context.Context, source, destination string, inventory func([]manifestFile), limits ImportLimits) (string, error) {
	if !limits.Valid() {
		return "", ErrInvalid
	}
	root, err := openDirectory(source)
	if err != nil {
		return "", ErrInvalid
	}
	defer root.Close()
	var entries []manifestFile
	visited := 0
	total := int64(0)
	var walk func(*os.File, string) error
	walk = func(directory *os.File, dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		children, err := directory.ReadDir(limits.Entries + 2)
		if err != nil && err != io.EOF || len(children) > limits.Entries+1 {
			return ErrInvalid
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := child.Name()
			if strings.EqualFold(name, ".git") {
				continue
			}
			// JSON would otherwise replace distinct invalid path bytes with
			// the same replacement character in the frozen manifest.
			if !utf8.ValidString(name) || strings.ContainsAny(name, "\r\n\x00") {
				return ErrInvalid
			}
			visited++
			relative := filepath.Join(dir, name)
			if visited > limits.Entries || len(relative) > 1024 {
				return ErrInvalid
			}
			parent := int(directory.Fd())
			var expected unix.Stat_t
			if err := unix.Fstatat(parent, name, &expected, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return ErrInvalid
			}
			flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
			isDir := expected.Mode&unix.S_IFMT == unix.S_IFDIR
			if isDir {
				flags |= unix.O_DIRECTORY
			} else if expected.Mode&unix.S_IFMT != unix.S_IFREG || expected.Nlink != 1 || expected.Size > int64(limits.FileBytes) {
				return ErrInvalid
			}
			fd, err := unix.Openat(parent, name, flags, 0)
			if err != nil {
				return ErrInvalid
			}
			file := os.NewFile(uintptr(fd), relative)
			var actual unix.Stat_t
			if err := unix.Fstat(fd, &actual); err != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino || actual.Mode&unix.S_IFMT != expected.Mode&unix.S_IFMT || !isDir && actual.Nlink != 1 {
				file.Close()
				return ErrInvalid
			}
			if isDir {
				if destination != "" {
					if err := os.Mkdir(filepath.Join(destination, relative), 0700); err != nil {
						file.Close()
						return ErrInvalid
					}
				}
				err = walk(file, relative)
				file.Close()
				if err != nil {
					return err
				}
				continue
			}
			data, err := io.ReadAll(io.LimitReader(file, int64(limits.FileBytes)+1))
			file.Close()
			if err != nil || len(data) > limits.FileBytes {
				return ErrInvalid
			}
			total += int64(len(data))
			if total > int64(limits.TreeBytes) {
				return ErrInvalid
			}
			mode := uint32(0600)
			if actual.Mode&0111 != 0 {
				mode = 0700
			}
			sum := sha256.Sum256(data)
			entries = append(entries, manifestFile{Name: filepath.ToSlash(relative), Hash: hex.EncodeToString(sum[:]), Mode: mode})
			if destination != "" {
				path := filepath.Join(destination, relative)
				if err := os.WriteFile(path, data, os.FileMode(mode)); err != nil {
					return ErrInvalid
				}
				file, err := os.Open(path)
				if err != nil {
					return ErrInvalid
				}
				err = file.Sync()
				file.Close()
				if err != nil {
					return ErrInvalid
				}
			}
		}
		if destination != "" {
			f, err := os.Open(filepath.Join(destination, dir))
			if err != nil {
				return ErrInvalid
			}
			err = f.Sync()
			f.Close()
			if err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
	if err := walk(root, "."); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(entries)
	if inventory != nil {
		inventory(entries)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Open each component relative to its already opened parent. A directory
// replaced with a symlink cannot redirect the next lookup, even within source.
func openDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrInvalid
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	if path == "/" {
		return os.NewFile(uintptr(fd), path), nil
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

// Location is derived from the canonical installation data directory and Task.
func Location(root, taskID string) Workspace {
	hash := sha256.Sum256([]byte(taskID))
	base := filepath.Join(root, hex.EncodeToString(hash[:16]))
	return Workspace{Directory: filepath.Join(base, "work"), Baseline: filepath.Join(base, "baseline")}
}

func private(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != filepath.Clean(path) {
		return ErrInvalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrInvalid
	}
	return nil
}
func lock(ctx context.Context, path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrInvalid
	}
	f := os.NewFile(uintptr(fd), "code workspace lock")
	for {
		if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) == nil {
			return f, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// PrepareWithLimits accepts only a narrower complete-import budget. It never
// filters files or silently publishes a partial source tree.
// It publishes baseline and working copy together in an immutable Task location;
// the receipt recovers a published directory without reading source again.
func PrepareWithLimits(ctx context.Context, root, source, taskID, scope string, boundary func(string), limits ImportLimits) (Workspace, error) {
	if !limits.Valid() {
		return Workspace{}, ErrInvalid
	}
	out := Location(root, taskID)
	base := filepath.Dir(out.Directory)
	name := filepath.Base(base)
	parent, err := openDirectory(filepath.Dir(root))
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	err = unix.Mkdirat(int(parent.Fd()), filepath.Base(root), 0700)
	parent.Close()
	if err != nil && err != unix.EEXIST {
		return Workspace{}, ErrInvalid
	}
	if err := private(root); err != nil {
		return Workspace{}, err
	}
	guard, err := lock(ctx, filepath.Join(root, "."+name+".lock"))
	if err != nil {
		return Workspace{}, err
	}
	defer guard.Close()
	out.Status = "ready"
	read := func() error {
		if err := private(base); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(base, "receipt.json"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return ErrInvalid
		}
		defer f.Close()
		var r receipt
		if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&r) != nil || r.Version != 1 || r.Scope != scope {
			return ErrInvalid
		}
		out.BaselineHash = r.BaselineHash
		actual, err := scanTree(ctx, out.Baseline, "", nil, limits)
		if err != nil || actual != r.BaselineHash {
			return ErrInvalid
		}
		return private(out.Directory)
	}
	if _, err := os.Lstat(base); err == nil {
		return out, read()
	} else if !os.IsNotExist(err) {
		return Workspace{}, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != filepath.Clean(source) {
		return Workspace{}, ErrInvalid
	}
	// The marker may be a directory or worktree file, but its bytes/config,
	// hooks, remotes and object databases are never opened.
	marker, err := os.Lstat(filepath.Join(source, ".git"))
	if err != nil || !(marker.IsDir() || marker.Mode().IsRegular()) {
		return Workspace{}, ErrInvalid
	}
	prefix := ".prepare-" + name + "-"
	stale, err := os.ReadDir(root)
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	for _, entry := range stale {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return Workspace{}, ErrInvalid
			}
		}
	}
	temp, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	defer os.RemoveAll(temp)
	for _, dir := range []string{"baseline", "work"} {
		if err := os.Mkdir(filepath.Join(temp, dir), 0700); err != nil {
			return Workspace{}, ErrInvalid
		}
	}
	baseline, err := scanTree(ctx, source, filepath.Join(temp, "baseline"), nil, limits)
	if err != nil {
		return Workspace{}, err
	}
	if current, err := scanTree(ctx, source, "", nil, limits); err != nil || current != baseline {
		return Workspace{}, ErrInvalid
	}
	copyHash, err := scanTree(ctx, filepath.Join(temp, "baseline"), filepath.Join(temp, "work"), nil, limits)
	if err != nil || copyHash != baseline {
		return Workspace{}, ErrInvalid
	}
	raw, _ := json.Marshal(receipt{Version: 1, Scope: scope, BaselineHash: baseline})
	if err := os.WriteFile(filepath.Join(temp, "receipt.json"), raw, 0600); err != nil {
		return Workspace{}, ErrInvalid
	}
	file, err := os.Open(filepath.Join(temp, "receipt.json"))
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	err = file.Sync()
	file.Close()
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	if boundary != nil {
		boundary("before_task_code_publish")
	}
	if err := ctx.Err(); err != nil {
		return Workspace{}, err
	}
	if err := os.Rename(temp, base); err != nil {
		return Workspace{}, ErrInvalid
	}
	dir, err := os.Open(root)
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	if boundary != nil {
		boundary("after_task_code_publish")
	}
	out.BaselineHash = baseline
	return out, nil
}

type Diff struct {
	Patch            string       `json:"patch"`
	PatchUnavailable string       `json:"patch_unavailable,omitempty"`
	Changed          bool         `json:"changed"`
	Files            []FileChange `json:"files"`
}
type FileChange struct {
	Path             string `json:"path"`
	Kind             string `json:"kind"`
	BeforeSHA256     string `json:"before_sha256,omitempty"`
	AfterSHA256      string `json:"after_sha256,omitempty"`
	BeforeExecutable bool   `json:"before_executable"`
	AfterExecutable  bool   `json:"after_executable"`
}

func Validate(ctx context.Context, w Workspace) error {
	if err := private(w.Directory); err != nil {
		return err
	}
	actual, err := scanTree(ctx, w.Baseline, "", nil, NativeImportLimits())
	if err != nil || actual != w.BaselineHash {
		return ErrInvalid
	}
	return nil
}

// ReviewWithPatchLimit rejects unsafe work-tree entries before a fixed read-only recursive
// diff in the existing kernel sandbox. It never executes repository commands.
func ReviewWithPatchLimit(ctx context.Context, w Workspace, patchBytes int) (Diff, error) {
	if patchBytes < 1 || patchBytes > 8<<20 {
		return Diff{}, ErrInvalid
	}
	guard, err := openDirectory(w.Directory)
	if err != nil {
		return Diff{}, ErrInvalid
	}
	defer guard.Close()
	if unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return Diff{}, ErrInvalid
	}
	temp, err := os.MkdirTemp("", "steward-code-review-")
	if err != nil {
		return Diff{}, ErrInvalid
	}
	defer os.RemoveAll(temp)
	temp, err = filepath.EvalSymlinks(temp)
	if err != nil {
		return Diff{}, ErrInvalid
	}
	left, right := filepath.Join(temp, "a"), filepath.Join(temp, "b")
	if os.Mkdir(left, 0700) != nil || os.Mkdir(right, 0700) != nil {
		return Diff{}, ErrInvalid
	}
	var before, after []manifestFile
	if actual, err := scanTree(ctx, w.Baseline, left, func(entries []manifestFile) { before = entries }, NativeImportLimits()); err != nil || actual != w.BaselineHash {
		return Diff{}, ErrInvalid
	}
	if _, err := scanTree(ctx, w.Directory, right, func(entries []manifestFile) { after = entries }, NativeImportLimits()); err != nil {
		return Diff{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd, err := sandbox.Command(ctx, "/usr/bin/diff", []string{"-ruN", "a", "b"}, nil, temp, temp)
	if err != nil {
		return Diff{}, ErrInvalid
	}
	cmd.Dir = temp
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + temp, "LANG=C.UTF-8"}
	output := sandbox.NewOutput(patchBytes)
	cmd.Stdout, cmd.Stderr = output, io.Discard
	started, err := sandbox.Run(cmd, unix.SIGTERM)
	if !started || output.Overflow() || ctx.Err() != nil || err != nil && cmd.ProcessState.ExitCode() != 1 {
		return Diff{}, ErrInvalid
	}
	files := map[string]FileChange{}
	for _, entry := range before {
		files[entry.Name] = FileChange{Path: entry.Name, BeforeSHA256: entry.Hash, BeforeExecutable: entry.Mode&0100 != 0}
	}
	for _, entry := range after {
		change := files[entry.Name]
		change.Path, change.AfterSHA256, change.AfterExecutable = entry.Name, entry.Hash, entry.Mode&0100 != 0
		files[entry.Name] = change
	}
	changes := []FileChange{}
	for _, change := range files {
		switch {
		case change.BeforeSHA256 == "":
			change.Kind = "added"
		case change.AfterSHA256 == "":
			change.Kind = "deleted"
		case change.BeforeSHA256 != change.AfterSHA256 || change.BeforeExecutable != change.AfterExecutable:
			change.Kind = "modified"
		default:
			continue
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	diff := Diff{Changed: len(changes) > 0, Files: changes}
	if patch := output.Bytes(); utf8.Valid(patch) {
		diff.Patch = string(patch)
	} else {
		diff.PatchUnavailable = "non_utf8"
	}
	return diff, nil
}
