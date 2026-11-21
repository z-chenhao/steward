package parserresource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"
)

// Launcher is trusted Host composition/test-carrier input. It never comes from
// a package, SDK request, source document or environment variable.
const MaxDocumentBytes = 8 << 20

type Process struct {
	Vault    Vault
	Launcher string
}
type Request struct {
	URL       string `json:"url"`
	MediaType string `json:"mediaType"`
	Data      []byte `json:"data"`
}
type Response struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

// Parse runs only a previously captured Node/module revision. This is pure
// computation over the original page; it cannot fetch a URL or choose a command.
func (p Process) Parse(ctx context.Context, id string, input Request) (Response, error) {
	var result Response
	if len(input.Data) > MaxDocumentBytes || (input.MediaType != "text/html" && input.MediaType != "application/pdf") || len(input.URL) > 4096 {
		return result, ErrInvalid
	}
	if _, err := p.Vault.Load(ctx, id); err != nil {
		return result, err
	}
	root, err := p.Vault.root()
	if err != nil {
		return result, err
	}
	root = filepath.Join(root, "revisions", name(id))
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	work, err := os.MkdirTemp("", "steward-web-parser-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		return result, err
	}
	launcher := p.Launcher
	if launcher == "" {
		launcher, err = os.Executable()
		if err != nil {
			return result, err
		}
	}
	launcher, err = filepath.EvalSymlinks(launcher)
	if err != nil {
		return result, err
	}
	command, cleanup, err := isolated(ctx, launcher, root, work)
	if err != nil {
		return result, err
	}
	defer cleanup()
	command.Dir = work
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + work, "TMPDIR=" + work, "NODE_OPTIONS=--no-global-search-paths --max-old-space-size=512"}
	raw, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	command.Stdin = bytes.NewReader(raw)
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		return result, err
	}
	if err = command.Start(); err != nil {
		return result, fmt.Errorf("WEB_EXTRACTOR_UNAVAILABLE: %w", err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Cancel()
			_ = command.Wait()
		}
	}()
	output, err := io.ReadAll(io.LimitReader(stdout, (8<<20)+1))
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil || len(output) > 8<<20 {
		return result, ErrInvalid
	}
	err = command.Wait()
	waited = true
	if err != nil {
		return result, fmt.Errorf("WEB_EXTRACTION_FAILED: %w", err)
	}
	// Preserve the existing script response semantics; original sources can
	// contain arbitrary text, but output must be bounded, valid UTF-8 and nonempty.
	if !utf8.Valid(output) || json.Unmarshal(output, &result) != nil || result.Error != "" || result.Text == "" {
		return Response{}, ErrInvalid
	}
	return result, nil
}
