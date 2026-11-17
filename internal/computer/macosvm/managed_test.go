package macosvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/llm/memory"
)

func TestManagedPreparationDeduplicatesCallersAndJoinsCancellation(t *testing.T) {
	m := &managedRuntime{}
	var calls atomic.Int32
	started, joined := make(chan struct{}), make(chan struct{})
	prepare := func(ctx context.Context) error {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		close(joined)
		return ctx.Err()
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if err := m.startPreparation(context.Background(), prepare); err != computer.ErrPreparing {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preparation did not start")
	}
	m.close()
	select {
	case <-joined:
	default:
		t.Fatal("shutdown returned before preparation joined")
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent callers installed multiple desktops", calls.Load())
	}
}

func TestManagedDesktopIsLazyAndUnsupportedHostsDoNotDownload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "desktop")
	p, err := New(DefaultConfig(filepath.Join(root, "store")), SSHGuest{Secrets: memory.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.WorkingDirectory() != "/Users/"+managedGuestUser+"/StewardWorkspace" {
		t.Fatal("managed workspace depends on unprepared Guest state", p.WorkingDirectory())
	}
	_, _ = ManagedState(root)
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("construction or settings read initialized desktop")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		if err = p.Prepare(context.Background()); err == nil {
			t.Fatal("unsupported host started desktop")
		}
		if _, err = os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("unsupported host wrote files")
		}
	}
}

func TestRuntimeArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, header := range []tar.Header{{Name: "../escape", Typeflag: tar.TypeReg, Size: 1}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp/escape"}} {
		var raw bytes.Buffer
		z := gzip.NewWriter(&raw)
		w := tar.NewWriter(z)
		if err := w.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			_, _ = w.Write([]byte("x"))
		}
		_ = w.Close()
		_ = z.Close()
		if err := extractRuntime(raw.Bytes(), t.TempDir()); err == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
}

func TestPinnedImageRejectsCorruptedAndIncompleteUnits(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	layer := func(name, text string) imageLayer {
		h := sha256.Sum256([]byte(text))
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		return imageLayer{Digest: "sha256:" + hex.EncodeToString(h[:]), Size: int64(len(text)), Annotations: map[string]string{"org.opencontainers.image.title": name}}
	}
	config := layer("config.json", `{"os":"macOS","hardwareModel":"hardware","machineIdentifier":"machine","cpuCount":4,"memorySize":8,"diskSize":4,"display":"1024x768","networkMode":"nat"}`)
	nvram := layer("nvram.bin", "firmware")
	disk := layer("disk.img", "disk")
	disk.Annotations = map[string]string{"org.opencontainers.image.title": "disk.img.part.0", "org.trycua.lume.part.offset": "0", "org.trycua.lume.content.uncompressed-size": "4", "org.trycua.lume.content.uncompressed-digest": disk.Digest}
	manifest := imageManifest{Config: config, ConfigData: []byte(`{"os":"macOS","hardwareModel":"hardware","machineIdentifier":"machine"}`), Annotations: map[string]string{"org.trycua.lume.cpu-count": "4", "org.trycua.lume.memory-size": "8", "org.trycua.lume.disk-size": "4", "org.trycua.lume.display": "1024x768"}, Layers: []imageLayer{nvram, disk}}
	if err := verifyImageFiles(context.Background(), root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "disk.img"), []byte("evil"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyImageFiles(context.Background(), root, manifest); err == nil {
		t.Fatal("corrupt disk accepted")
	}
	manifest.Layers = manifest.Layers[:1]
	if err := verifyImageFiles(context.Background(), root, manifest); err == nil {
		t.Fatal("incomplete manifest accepted")
	}
}
