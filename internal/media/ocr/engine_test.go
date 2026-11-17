package ocr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/media"
)

func installedEngine(t *testing.T) *Engine {
	t.Helper()
	e := Discover(context.Background())
	if !e.Available() {
		t.Skip("Tesseract, supported language data and kernel sandbox are required")
	}
	return e
}

// This uses actual image pixels and the installed offline engine, not PNG
// metadata or a scripted transcription. It proves plumbing, not OCR quality.
func TestActualTesseractPixelsAndFrozenResources(t *testing.T) {
	e := installedEngine(t)
	pixels, err := os.ReadFile("testdata/text.png")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	doc, err := e.Extract(ctx, "image/png", pixels)
	if err != nil || !strings.Contains(doc.Text, "OCR 12345") || doc.Width != 640 || doc.Height != 120 || len(doc.Words) < 2 || doc.Method != "ocr" {
		t.Fatalf("actual OCR: %+v, %v", doc, err)
	}
	for _, word := range doc.Words {
		if word.X < 0 || word.Y < 0 || word.X+word.Width > doc.Width || word.Y+word.Height > doc.Height {
			t.Fatal("box outside original pixels", word)
		}
	}
	// Copies are verified against the frozen pack hash, even when installation
	// paths stay the same. The old engine must not run replacement data.
	pack := filepath.Join(t.TempDir(), "eng.traineddata")
	if err := os.WriteFile(pack, []byte("changed language data"), 0600); err != nil {
		t.Fatal(err)
	}
	old := *e
	old.languages = append([]language(nil), e.languages...)
	old.languages[0].Path = pack
	if _, err = old.Extract(ctx, "image/png", pixels); !errors.Is(err, ErrChanged) {
		t.Fatal("changed pack accepted", err)
	}
	old = *e
	old.binaryHash = "changed"
	if _, err = old.Extract(ctx, "image/png", pixels); !errors.Is(err, ErrChanged) {
		t.Fatal("changed engine accepted", err)
	}
	if _, err = e.Extract(ctx, "image/png", []byte("broken")); !errors.Is(err, media.ErrInvalid) {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = e.Extract(cancelled, "image/png", pixels); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDeterministicOCRDiscovery(t *testing.T) {
	e := installedEngine(t)
	if other := Discover(context.Background()); other.ResourceHash() != e.ResourceHash() {
		t.Fatal("unchanged installation changed identity")
	}
}

func TestRawOCRRejectsPluginOptionsOutsideFrozenEngine(t *testing.T) {
	e := installedEngine(t)
	pixels, err := os.ReadFile("testdata/text.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []Plan{{Languages: e.Languages(), Layout: 99}, {Languages: []string{"../../private"}, Layout: 3}, {Languages: []string{"eng", "eng"}, Layout: 3}, {Layout: 3}} {
		if _, err := e.Raw(context.Background(), "image/png", pixels, plan); !errors.Is(err, media.ErrInvalid) {
			t.Fatal("unscoped language/layout accepted", plan, err)
		}
	}
}

func TestMissingOCR(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	missing := Discover(context.Background())
	if missing.Available() || missing.ResourceHash() != (*Engine)(nil).ResourceHash() {
		t.Fatal("implicit OCR fallback")
	}
	if _, err := missing.Extract(context.Background(), "image/png", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestOCRMetadataOutputCap(t *testing.T) {
	if _, err := commandOutput(context.Background(), "/bin/bash", "-c", "head -c 100000 /dev/zero"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("subprocess output bypassed cap", err)
	}
}

func TestOCRCancellationStopsStartedSandboxAndCleansJob(t *testing.T) {
	e := *installedEngine(t)
	// A controlled installed engine blocks after the real sandbox starts. This
	// checks cancellation/cleanup without depending on OCR workload duration.
	executable := filepath.Join(t.TempDir(), "ocr-block")
	script := `#!/bin/bash
if [ "$1" = --version ]; then echo ocr-cancel-test; exit 0; fi
job="${4%/data}"
echo started > "$job/started"
sleep 30 &
wait
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Discover records canonical executable paths in the frozen identity.
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	e.executable = canonical
	e.version = "ocr-cancel-test\n"
	e.binaryHash, err = fileHash(canonical)
	if err != nil {
		t.Fatal(err)
	}
	jobs := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(jobs, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)
	pixels, err := os.ReadFile("testdata/text.png")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.Extract(ctx, "image/png", pixels); done <- err }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
waiting:
	for {
		select {
		case err := <-done:
			t.Fatal("sandbox exited before cancellation", err)
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("sandbox did not start")
		case <-tick.C:
			paths, _ := filepath.Glob(filepath.Join(jobs, "steward-ocr-*", "started"))
			if len(paths) == 1 {
				break waiting
			}
		}
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled OCR did not join its process")
	}
	remaining, err := os.ReadDir(jobs)
	if err != nil || len(remaining) != 0 {
		t.Fatal("OCR job retained after cancellation", remaining, err)
	}
}
