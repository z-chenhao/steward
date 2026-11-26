package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
)

type testBuildEntry struct {
	once sync.Once
	path string
	err  error
}

var testBinaryCache struct {
	mu      sync.Mutex
	dir     string
	entries map[string]*testBuildEntry
}

func cleanupTestBinaryCache() error {
	testBinaryCache.mu.Lock()
	dir := testBinaryCache.dir
	testBinaryCache.dir = ""
	testBinaryCache.entries = nil
	testBinaryCache.mu.Unlock()
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

func testBinaryCacheKey(pkg string) (string, error) {
	abs, err := filepath.Abs(pkg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs + "\x00" + runtime.GOOS + "\x00" + runtime.GOARCH))
	return hex.EncodeToString(sum[:16]), nil
}

func ensureCachedTestBinary(pkg string) (string, error) {
	key, err := testBinaryCacheKey(pkg)
	if err != nil {
		return "", err
	}
	testBinaryCache.mu.Lock()
	if testBinaryCache.entries == nil {
		testBinaryCache.entries = map[string]*testBuildEntry{}
	}
	entry := testBinaryCache.entries[key]
	if entry == nil {
		entry = &testBuildEntry{}
		testBinaryCache.entries[key] = entry
	}
	testBinaryCache.mu.Unlock()

	entry.once.Do(func() {
		cacheDir := os.Getenv("STEWARD_TEST_BUILD_CACHE_DIR")
		shared := cacheDir != ""
		if !shared {
			testBinaryCache.mu.Lock()
			if testBinaryCache.dir == "" {
				testBinaryCache.dir, entry.err = os.MkdirTemp("", "steward-app-test-bins-")
			}
			cacheDir = testBinaryCache.dir
			testBinaryCache.mu.Unlock()
			if entry.err != nil {
				return
			}
		} else if err := os.MkdirAll(cacheDir, 0700); err != nil {
			entry.err = err
			return
		}
		target := filepath.Join(cacheDir, key+".bin")
		if shared {
			if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
				entry.path = target
				return
			}
			lockPath := filepath.Join(cacheDir, key+".lock")
			lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				entry.err = err
				return
			}
			defer lockFile.Close()
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
				entry.err = err
				return
			}
			defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
			if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
				entry.path = target
				return
			}
		}
		tmpFile, err := os.CreateTemp(cacheDir, key+".tmp-*")
		if err != nil {
			entry.err = err
			return
		}
		tmpPath := tmpFile.Name()
		_ = tmpFile.Close()
		defer os.Remove(tmpPath)
		if out, err := exec.Command("go", "build", "-o", tmpPath, pkg).CombinedOutput(); err != nil {
			entry.err = fmt.Errorf("go build %s: %w: %s", pkg, err, out)
			return
		}
		if err := os.Chmod(tmpPath, 0700); err != nil {
			entry.err = err
			return
		}
		if err := os.Rename(tmpPath, target); err != nil {
			entry.err = err
			return
		}
		entry.path = target
	})
	return entry.path, entry.err
}

// buildTestBinary compiles an unmodified Go package binary once per test process
// (or once per disposable STEWARD_TEST_BUILD_CACHE_DIR) and writes an
// independent executable copy to dst.
func buildTestBinary(t *testing.T, pkg, dst string) {
	t.Helper()
	src, err := ensureCachedTestBinary(pkg)
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
