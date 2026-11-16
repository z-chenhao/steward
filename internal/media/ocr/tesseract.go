package ocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/internal/media"
)

// Plan contains only fixed local engine options. No argument, executable or
// path can be supplied by a model or plugin.
type Plan struct {
	Languages []string `json:"languages"`
	Layout    int      `json:"layout"`
}
type RawOutput struct {
	TSV           []byte
	Width, Height int
	Languages     []string
}

func (e *Engine) Extract(ctx context.Context, kind string, data []byte) (Document, error) {
	output, err := e.Raw(ctx, kind, data, Plan{Languages: e.Languages(), Layout: 3})
	if err != nil {
		return Document{}, err
	}
	return parseTSV(output.TSV, output.Width, output.Height, output.Languages)
}

// Raw is a trusted, local read-only compute primitive. Parsing and recognition
// policy can live in a versioned feature package; execution stays sandboxed.
func (e *Engine) Raw(ctx context.Context, kind string, data []byte, plan Plan) (RawOutput, error) {
	if err := ctx.Err(); err != nil {
		return RawOutput{}, err
	}
	if err := e.verify(ctx); err != nil {
		return RawOutput{}, err
	}
	if plan.Layout != 3 && plan.Layout != 6 && plan.Layout != 7 && plan.Layout != 11 && plan.Layout != 12 {
		return RawOutput{}, media.ErrInvalid
	}
	if len(plan.Languages) == 0 || len(plan.Languages) > len(e.languages) {
		return RawOutput{}, media.ErrInvalid
	}
	seen := map[string]bool{}
	for _, name := range plan.Languages {
		found := false
		for _, lang := range e.languages {
			found = found || lang.Name == name
		}
		if !found || seen[name] {
			return RawOutput{}, media.ErrInvalid
		}
		seen[name] = true
	}
	img, err := media.DecodeImage(kind, data)
	if err != nil {
		return RawOutput{}, err
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	// Seatbelt matches the canonical filesystem path. The system temp
	// directory can be an alias such as /var on macOS.
	temp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return RawOutput{}, ErrUnavailable
	}
	temp, err = filepath.EvalSymlinks(temp)
	if err != nil {
		return RawOutput{}, ErrUnavailable
	}
	root, err := os.MkdirTemp(temp, "steward-ocr-")
	if err != nil {
		return RawOutput{}, ErrUnavailable
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "data")
	if os.Mkdir(directory, 0700) != nil {
		return RawOutput{}, ErrUnavailable
	}
	for _, lang := range e.languages {
		if err := ctx.Err(); err != nil {
			return RawOutput{}, err
		}
		path := filepath.Join(directory, lang.Name+".traineddata")
		if err := copyLanguage(lang, path); err != nil {
			return RawOutput{}, err
		}
	}
	readRoots := []string{}
	if strings.HasPrefix(e.executable, "/opt/homebrew/") {
		readRoots = append(readRoots, "/opt/homebrew")
	}
	args := []string{"stdin", "stdout", "--tessdata-dir", directory, "-l", strings.Join(plan.Languages, "+"), "--oem", "1", "--psm", strconv.Itoa(plan.Layout), "-c", "tessedit_create_tsv=1"}
	cmd, err := sandbox.Command(ctx, e.executable, args, readRoots, root, root)
	if err != nil {
		return RawOutput{}, ErrUnavailable
	}
	cmd.Env = cleanEnv()
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(data)
	output := sandbox.NewOutput(2 << 20)
	cmd.Stdout = output
	_, err = sandbox.Run(cmd, syscall.SIGKILL)
	if ctx.Err() != nil {
		return RawOutput{}, ctx.Err()
	}
	if output.Overflow() {
		return RawOutput{}, media.ErrLimit
	}
	if err != nil {
		return RawOutput{}, ErrUnavailable
	}
	if err = e.verify(ctx); err != nil {
		return RawOutput{}, err
	}
	return RawOutput{TSV: output.Bytes(), Width: width, Height: height, Languages: append([]string(nil), plan.Languages...)}, nil
}

func copyLanguage(lang language, path string) error {
	source, err := os.Open(lang.Path)
	if err != nil {
		return ErrChanged
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return ErrChanged
	}
	dest, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return ErrUnavailable
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dest, hash), io.LimitReader(source, (64<<20)+1))
	closed := dest.Close()
	if err != nil || closed != nil {
		return ErrUnavailable
	}
	if n > 64<<20 || hex.EncodeToString(hash.Sum(nil)) != lang.Hash {
		return ErrChanged
	}
	return nil
}
