package webcontent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Exact pre-extract-service/unbundled or previously selected nineteen-package execution boundary. Never used
// after a managed document decoder is bound; remove when those consumers end.
func extractDocumentLegacyV1(ctx context.Context, c Config, source, kind string, data []byte) (string, string, error) {
	if c.ExtractScript == "" {
		if kind == "text/html" {
			return ParseHTML(ctx, data)
		}
		return "", "", errors.New("WEB_EXTRACTOR_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	root, e := os.MkdirTemp("", "steward-web-extract-")
	if e != nil {
		return "", "", e
	}
	defer os.RemoveAll(root)
	raw, _ := json.Marshal(struct {
		URL       string `json:"url"`
		MediaType string `json:"mediaType"`
		Data      []byte `json:"data"`
	}{source, kind, data})
	command := exec.CommandContext(ctx, c.ExtractCommand, c.ExtractScript)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	command.Dir = root
	command.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=" + filepath.Dir(c.ExtractCommand) + ":/usr/bin:/bin", "LANG=C.UTF-8"}
	command.Stdin = bytes.NewReader(raw)
	command.Stderr = io.Discard
	stdout, e := command.StdoutPipe()
	if e != nil {
		return "", "", e
	}
	if command.Start() != nil {
		return "", "", errors.New("WEB_EXTRACTOR_UNAVAILABLE")
	}
	defer func() { _ = command.Cancel(); _ = command.Wait() }()
	result, e := io.ReadAll(io.LimitReader(stdout, (8<<20)+1))
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	if e != nil || len(result) > 8<<20 {
		return "", "", errors.New("RESOURCE_LIMIT")
	}
	var response struct {
		Title string `json:"title"`
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if json.Unmarshal(result, &response) != nil || response.Error != "" || response.Text == "" {
		return "", "", errors.New("WEB_EXTRACTION_FAILED")
	}
	return response.Title, response.Text, nil
}
