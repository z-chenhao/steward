package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReviewSnapshotIdentifiesExactFilesAndExecutableModes(t *testing.T) {
	ctx := context.Background()
	source, root := paths(t)
	w, err := PrepareWithLimits(ctx, root, source, "snapshot", "scope", nil, NativeImportLimits())
	must(t, err)
	capture := func() *CandidateSnapshot {
		t.Helper()
		candidate, err := CaptureCandidate(ctx, w)
		must(t, err)
		t.Cleanup(func() { must(t, candidate.Close()) })
		return candidate
	}
	first := capture()
	firstID := first.SHA256
	if firstID != w.BaselineHash {
		t.Fatal("initial identity does not describe the imported baseline", first)
	}
	again := capture()
	if again.SHA256 != firstID {
		t.Fatal("unchanged candidate identity is not reproducible")
	}
	seen := map[string]bool{firstID: true}
	for _, mutate := range []func(){
		func() { must(t, os.WriteFile(filepath.Join(w.Directory, "notes.txt"), []byte("new input"), 0600)) },
		func() { must(t, os.Chmod(filepath.Join(w.Directory, "notes.txt"), 0700)) },
		func() { must(t, os.WriteFile(filepath.Join(w.Directory, "empty"), nil, 0600)) },
		func() { must(t, os.WriteFile(filepath.Join(w.Directory, "binary"), []byte{0, 255, 1}, 0600)) },
		func() { must(t, os.Remove(filepath.Join(w.Directory, "notes.txt"))) },
	} {
		mutate()
		candidate := capture()
		identity := candidate.SHA256
		if len(identity) != 64 || seen[identity] {
			t.Fatal("changed candidate reused a previous identity", candidate)
		}
		seen[identity] = true
	}
	data, err := os.ReadFile(filepath.Join(first.Directory, "notes.txt"))
	must(t, err)
	if string(data) != "owner dirty content\n" {
		t.Fatal("a later edit changed the frozen candidate", string(data))
	}
}

func TestCandidateSnapshotRejectsUnsafeBusyAndCancelledInputsWithoutResidue(t *testing.T) {
	source, root := paths(t)
	w, err := PrepareWithLimits(context.Background(), root, source, "candidate-boundary", "scope", nil, NativeImportLimits())
	must(t, err)
	assertClean := func() {
		t.Helper()
		entries, err := os.ReadDir(filepath.Dir(w.Directory))
		must(t, err)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "candidate-") {
				t.Fatal("failed capture left a private candidate", entry.Name())
			}
		}
		data, err := os.ReadFile(filepath.Join(w.Directory, "notes.txt"))
		must(t, err)
		if string(data) != "owner dirty content\n" {
			t.Fatal("cleanup changed the task working copy")
		}
	}
	guard, err := openDirectory(w.Directory)
	must(t, err)
	must(t, unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	if _, err := CaptureCandidate(context.Background(), w); err == nil {
		t.Fatal("captured while an execution owns the workspace")
	}
	must(t, guard.Close())
	assertClean()
	must(t, os.Symlink("/etc/passwd", filepath.Join(w.Directory, "outside")))
	if _, err := CaptureCandidate(context.Background(), w); err == nil {
		t.Fatal("unsafe file entered a candidate")
	}
	must(t, os.Remove(filepath.Join(w.Directory, "outside")))
	assertClean()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureCandidate(ctx, w); err == nil {
		t.Fatal("cancelled capture returned a candidate")
	}
	assertClean()
}
