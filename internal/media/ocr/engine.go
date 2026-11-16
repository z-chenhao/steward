// Package ocr executes a frozen local OCR engine, without model credentials or
// network access. Discovery is optional; a missing installation never enables a
// fallback provider or blocks ordinary conversation.
package ocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
)

var ErrUnavailable = errors.New("local OCR unavailable")
var ErrChanged = errors.New("OCR resources changed")

type language struct{ Name, Path, Hash string }
type Engine struct {
	executable, binaryHash, version string
	languages                       []language
	hash                            string
}

func cleanEnv() []string { return []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OMP_THREAD_LIMIT=1"} }

func commandOutput(ctx context.Context, exe string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = cleanEnv()
	output := sandbox.NewOutput(32 << 10)
	cmd.Stdout = output
	if _, err := sandbox.Run(cmd, syscall.SIGKILL); err != nil || output.Overflow() {
		return nil, ErrUnavailable
	}
	return output.Bytes(), nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return "", ErrUnavailable
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (64<<20)+1))
	if err != nil || n > 64<<20 {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Discover(ctx context.Context) *Engine {
	missing := &Engine{hash: "image-ocr-v1:unavailable"}
	if sandbox.Check() != nil {
		return missing
	}
	exe, err := exec.LookPath("tesseract")
	if err != nil {
		return missing
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil || !filepath.IsAbs(exe) {
		return missing
	}
	version, err := commandOutput(ctx, exe, "--version")
	if err != nil {
		return missing
	}
	args := []string{"--list-langs"}
	if root := os.Getenv("TESSDATA_PREFIX"); root != "" {
		if !filepath.IsAbs(root) {
			return missing
		}
		args = append(args, "--tessdata-dir", root)
	}
	listed, err := commandOutput(ctx, exe, args...)
	if err != nil {
		return missing
	}
	lines := strings.Split(strings.TrimSpace(string(listed)), "\n")
	if len(lines) < 2 {
		return missing
	}
	_, tail, ok := strings.Cut(lines[0], "\"")
	if !ok {
		return missing
	}
	root, _, ok := strings.Cut(tail, "\"")
	if !ok || !filepath.IsAbs(root) {
		return missing
	}
	e := &Engine{executable: exe, version: string(version)}
	e.binaryHash, err = fileHash(exe)
	if err != nil {
		return missing
	}
	for _, name := range lines[1:] {
		name = strings.TrimSpace(name)
		if name != "eng" && name != "chi_sim" && name != "chi_tra" {
			continue
		}
		path := filepath.Join(root, name+".traineddata")
		hash, err := fileHash(path)
		if err != nil {
			return missing
		}
		e.languages = append(e.languages, language{name, path, hash})
	}
	if len(e.languages) == 0 {
		return missing
	}
	sort.Slice(e.languages, func(i, j int) bool { return e.languages[i].Name < e.languages[j].Name })
	identity, _ := json.Marshal(struct {
		Version, Binary, Parameters string
		Languages                   []language
	}{e.version, e.binaryHash, "lstm:psm3:tsv:v1", e.languages})
	sum := sha256.Sum256(identity)
	e.hash = "image-ocr-v1:" + hex.EncodeToString(sum[:])
	return e
}

func (e *Engine) ResourceHash() string {
	if e == nil {
		return "image-ocr-v1:unavailable"
	}
	return e.hash
}
func (e *Engine) Languages() []string {
	out := []string{}
	if e != nil {
		for _, lang := range e.languages {
			out = append(out, lang.Name)
		}
	}
	return out
}
func (e *Engine) Available() bool { return e != nil && len(e.languages) > 0 }

func (e *Engine) verify(ctx context.Context) error {
	if !e.Available() {
		return ErrUnavailable
	}
	hash, err := fileHash(e.executable)
	if err != nil || hash != e.binaryHash {
		return ErrChanged
	}
	version, err := commandOutput(ctx, e.executable, "--version")
	if err != nil || !bytes.Equal(version, []byte(e.version)) {
		return ErrChanged
	}
	return nil
}

// Check verifies the frozen native resources without running recognition.
func (e *Engine) Check(ctx context.Context) error {
	if err := e.verify(ctx); err != nil {
		return err
	}
	for _, lang := range e.languages {
		if err := ctx.Err(); err != nil {
			return err
		}
		hash, err := fileHash(lang.Path)
		if err != nil || hash != lang.Hash {
			return ErrChanged
		}
	}
	return nil
}
