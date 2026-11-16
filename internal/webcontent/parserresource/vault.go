// Package parserresource owns immutable resources for the existing local Node
// HTML/PDF decoder. It is a private Host mechanism, not a general exec API.
package parserresource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const MaxEngineBytes = 256 << 20 // The pinned Node24 artifact is ~122 MiB.
const MaxModuleBytes = 16 << 20  // Complete current HTML/PDF bundle is ~4 MiB.

var ErrInvalid = errors.New("WEB_PARSER_RESOURCE_INVALID")
var ErrChanged = errors.New("WEB_PARSER_RESOURCE_CHANGED")
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Vault struct{ Root string }
type Revision struct {
	EngineDigest string `json:"engine_digest"`
	ModuleDigest string `json:"module_digest"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func (r Revision) Digest() string { raw, _ := json.Marshal(r); return digest(raw) }
func name(value string) string    { return value[len("sha256:"):] }
func (v Vault) root() (string, error) {
	if !filepath.IsAbs(v.Root) {
		return "", ErrInvalid
	}
	if err := os.MkdirAll(v.Root, 0700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(v.Root)
}

// Capture is called by the trusted resource owner before binding a revision.
// module must be a complete dependency bundle; no ambient node_modules folder
// or source-directory grant is retained. The module bytes remain replaceable
// plugin content; the interpreter is an independently identified Host resource.
func (v Vault) Capture(ctx context.Context, command string, module []byte) (Revision, error) {
	var revision Revision
	if !filepath.IsAbs(command) || len(module) == 0 || len(module) > MaxModuleBytes {
		return revision, ErrInvalid
	}
	root, err := v.root()
	if err != nil {
		return revision, err
	}
	command, err = filepath.EvalSymlinks(command)
	if err != nil {
		return revision, err
	}
	input, err := os.Open(command)
	if err != nil {
		return revision, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > MaxEngineBytes {
		return revision, ErrInvalid
	}
	work, err := os.MkdirTemp(root, ".capture-")
	if err != nil {
		return revision, err
	}
	defer os.RemoveAll(work)
	enginePath := filepath.Join(work, "node")
	output, err := os.OpenFile(enginePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		return revision, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&contextReader{ctx: ctx, reader: input}, MaxEngineBytes+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return revision, copyErr
	}
	if syncErr != nil {
		return revision, syncErr
	}
	if closeErr != nil {
		return revision, closeErr
	}
	if n > MaxEngineBytes || n != info.Size() {
		return revision, ErrInvalid
	}
	revision = Revision{EngineDigest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), ModuleDigest: digest(module)}
	if err = writeSync(filepath.Join(work, "parser.mjs"), module, 0400); err != nil {
		return Revision{}, err
	}
	metadata, _ := json.Marshal(revision)
	if err = writeSync(filepath.Join(work, "revision.json"), metadata, 0400); err != nil {
		return Revision{}, err
	}
	// Share the large immutable interpreter across parser revisions. Existing
	// objects are checked; an install never overwrites a retained engine inode.
	engines := filepath.Join(root, "engines")
	revisions := filepath.Join(root, "revisions")
	if err = os.MkdirAll(engines, 0700); err != nil {
		return Revision{}, err
	}
	if err = os.MkdirAll(revisions, 0700); err != nil {
		return Revision{}, err
	}
	engine := filepath.Join(engines, name(revision.EngineDigest))
	if err = os.Link(enginePath, engine); err != nil && !errors.Is(err, os.ErrExist) {
		return Revision{}, err
	}
	if err = checkFile(ctx, engine, revision.EngineDigest, MaxEngineBytes, true); err != nil {
		return Revision{}, err
	}
	if err = os.Remove(enginePath); err != nil {
		return Revision{}, err
	}
	if err = os.Link(engine, enginePath); err != nil {
		return Revision{}, err
	}
	destination := filepath.Join(revisions, name(revision.Digest()))
	if err = os.Rename(work, destination); err != nil {
		if _, loadErr := v.Load(ctx, revision.Digest()); loadErr != nil {
			return Revision{}, err
		}
	}
	// Sync the parent after rename; no mutable index or second Run ledger.
	directory, err := os.Open(revisions)
	if err != nil {
		return Revision{}, err
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return Revision{}, err
	}
	for _, directory := range []string{engines, root, filepath.Dir(root)} {
		file, e := os.Open(directory)
		if e != nil {
			return Revision{}, e
		}
		e = file.Sync()
		_ = file.Close()
		if e != nil {
			return Revision{}, e
		}
	}
	_, err = v.Load(ctx, revision.Digest())
	return revision, err
}

func writeSync(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	end := f.Close()
	if err != nil {
		return err
	}
	return end
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func checkFile(ctx context.Context, path, want string, limit int64, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit || (executable && info.Mode().Perm()&0111 == 0) {
		return ErrChanged
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrChanged
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: f}, limit+1))
	if err != nil {
		return err
	}
	if n > limit || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != want {
		return ErrChanged
	}
	return nil
}

// Load validates retained bytes instead of reopening the configured executable
// or dependency installation. The caller still owns current authorization.
func (v Vault) Load(ctx context.Context, id string) (Revision, error) {
	var revision Revision
	if !digestPattern.MatchString(id) {
		return revision, ErrInvalid
	}
	root, err := v.root()
	if err != nil {
		return revision, err
	}
	directory := filepath.Join(root, "revisions", name(id))
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return revision, ErrChanged
	}
	metadataPath := filepath.Join(directory, "revision.json")
	info, err = os.Lstat(metadataPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return revision, ErrChanged
	}
	raw, err := os.ReadFile(metadataPath)
	if err != nil || json.Unmarshal(raw, &revision) != nil {
		return revision, ErrChanged
	}
	canonical, _ := json.Marshal(revision)
	if !bytes.Equal(raw, canonical) || !digestPattern.MatchString(revision.EngineDigest) || !digestPattern.MatchString(revision.ModuleDigest) || revision.Digest() != id {
		return Revision{}, ErrChanged
	}
	if err = checkFile(ctx, filepath.Join(directory, "node"), revision.EngineDigest, MaxEngineBytes, true); err != nil {
		return Revision{}, err
	}
	if err = checkFile(ctx, filepath.Join(directory, "parser.mjs"), revision.ModuleDigest, MaxModuleBytes, false); err != nil {
		return Revision{}, err
	}
	return revision, nil
}

// Derive keeps a captured interpreter while selecting a new sealed plugin
// module. It never resolves the current configured command again.
func (v Vault) Derive(ctx context.Context, base string, module []byte) (Revision, error) {
	if _, err := v.Load(ctx, base); err != nil {
		return Revision{}, err
	}
	root, err := v.root()
	if err != nil {
		return Revision{}, err
	}
	return v.Capture(ctx, filepath.Join(root, "revisions", name(base), "node"), module)
}
