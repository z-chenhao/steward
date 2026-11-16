package coding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"golang.org/x/sys/unix"
)

func paths(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git", "hooks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("owner dirty content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("CREDENTIAL_METADATA_MUST_NOT_BE_COPIED"), 0600); err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(root, "execution")
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndependentWorkingCopiesRetainDirtyBaselineAndRecover(t *testing.T) {
	source, root := paths(t)
	ctx := context.Background()
	a, err := PrepareWithLimits(ctx, root, source, "a", "frozen", nil, NativeImportLimits())
	must(t, err)
	b, err := PrepareWithLimits(ctx, root, source, "b", "frozen", nil, NativeImportLimits())
	must(t, err)
	if a.Directory == b.Directory || a.BaselineHash != b.BaselineHash {
		t.Fatal(a, b)
	}
	must(t, os.WriteFile(filepath.Join(a.Directory, "notes.txt"), []byte("Task A edit\n"), 0600))
	for _, dir := range []string{source, b.Directory, a.Baseline} {
		raw, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		must(t, err)
		if string(raw) != "owner dirty content\n" {
			t.Fatal("work polluted source or peer", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Directory, ".git")); !os.IsNotExist(err) {
		t.Fatal("source Git metadata imported")
	}
	must(t, os.WriteFile(filepath.Join(source, "notes.txt"), []byte("later owner edit\n"), 0600))
	again, err := PrepareWithLimits(ctx, root, source, "a", "frozen", nil, NativeImportLimits())
	must(t, err)
	if again != a {
		t.Fatal("restart rebound snapshot", again, a)
	}
	if _, err := PrepareWithLimits(ctx, root, source, "a", "changed-scope", nil, NativeImportLimits()); err == nil {
		t.Fatal("changed frozen scope accepted")
	}
}

func TestUnsafeSourceAndChangedBaselineFailClosed(t *testing.T) {
	source, root := paths(t)
	ctx := context.Background()
	outside := filepath.Join(filepath.Dir(source), "private")
	must(t, os.WriteFile(outside, []byte("private"), 0600))
	must(t, os.Symlink(outside, filepath.Join(source, "link")))
	if _, err := PrepareWithLimits(ctx, root, source, "bad", "scope", nil, NativeImportLimits()); err == nil {
		t.Fatal("source symlink imported")
	}
	must(t, os.Remove(filepath.Join(source, "link")))
	must(t, os.Symlink(filepath.Join(source, ".git"), filepath.Join(source, "nested-git")))
	if _, err := PrepareWithLimits(ctx, root, source, "bad", "scope", nil, NativeImportLimits()); err == nil {
		t.Fatal("directory alias could import Git metadata")
	}
	must(t, os.Remove(filepath.Join(source, "nested-git")))
	must(t, os.Link(outside, filepath.Join(source, "hardlink")))
	if _, err := PrepareWithLimits(ctx, root, source, "bad", "scope", nil, NativeImportLimits()); err == nil {
		t.Fatal("source hardlink imported")
	}
	must(t, os.Remove(filepath.Join(source, "hardlink")))
	w, err := PrepareWithLimits(ctx, root, source, "bad", "scope", nil, NativeImportLimits())
	must(t, err)
	must(t, os.WriteFile(filepath.Join(w.Baseline, "notes.txt"), []byte("changed baseline"), 0600))
	if _, err := PrepareWithLimits(ctx, root, source, "bad", "scope", nil, NativeImportLimits()); err == nil {
		t.Fatal("tampered baseline reused")
	}
	if _, err := ReviewWithPatchLimit(ctx, w, 8<<20); err == nil {
		t.Fatal("tampered baseline reported no differences")
	}
}

func TestSandboxReviewProducesRelativePatchAndRefusesLinks(t *testing.T) {
	source, root := paths(t)
	ctx := context.Background()
	w, err := PrepareWithLimits(ctx, root, source, "review", "scope", nil, NativeImportLimits())
	must(t, err)
	must(t, os.WriteFile(filepath.Join(w.Directory, "notes.txt"), []byte("Task edit\n"), 0700))
	must(t, os.Chmod(filepath.Join(w.Directory, "notes.txt"), 0700))
	must(t, os.WriteFile(filepath.Join(w.Directory, "new.txt"), []byte("new file\n"), 0600))
	diff, err := ReviewWithPatchLimit(ctx, w, 8<<20)
	must(t, err)
	if !diff.Changed || !strings.Contains(diff.Patch, "--- a/notes.txt") || !strings.Contains(diff.Patch, "+++ b/notes.txt") || strings.Contains(diff.Patch, root) || len(diff.Files) != 2 {
		t.Fatal(diff)
	}
	must(t, os.Symlink("/etc/passwd", filepath.Join(w.Directory, "escape")))
	if _, err := ReviewWithPatchLimit(ctx, w, 8<<20); err == nil {
		t.Fatal("unsafe changed tree reviewed as ordinary diff")
	}
}

func TestReviewInventoryIncludesEmptyFilesAndKeepsPatchContent(t *testing.T) {
	source, root := paths(t)
	location := Location(root, "inventory")
	old := "-- " + location.Baseline + "/literal\n"
	changed := "++ " + location.Directory + "/literal\n"
	must(t, os.WriteFile(filepath.Join(source, "lines.txt"), []byte(old), 0600))
	must(t, os.WriteFile(filepath.Join(source, "empty-removed"), nil, 0600))
	w, err := PrepareWithLimits(context.Background(), root, source, "inventory", "scope", nil, NativeImportLimits())
	must(t, err)
	must(t, os.WriteFile(filepath.Join(w.Directory, "lines.txt"), []byte(changed), 0600))
	must(t, os.WriteFile(filepath.Join(w.Directory, "empty-added"), nil, 0600))
	must(t, os.Remove(filepath.Join(w.Directory, "empty-removed")))
	diff, err := ReviewWithPatchLimit(context.Background(), w, 8<<20)
	must(t, err)
	if !diff.Changed || len(diff.Files) != 3 || diff.Files[0].Path != "empty-added" || diff.Files[0].Kind != "added" || diff.Files[1].Path != "empty-removed" || diff.Files[1].Kind != "deleted" || !strings.Contains(diff.Patch, "-"+old) || !strings.Contains(diff.Patch, "+"+changed) {
		t.Fatal(diff)
	}
}

func TestTaskTreeRejectsInvalidUTF8Filename(t *testing.T) {
	source, root := paths(t)
	ctx := context.Background()
	name := "entry-\xff.txt"
	invalid := filepath.Join(source, name)
	if err := os.WriteFile(invalid, []byte("file"), 0600); err != nil {
		if errors.Is(err, unix.EILSEQ) || errors.Is(err, unix.EINVAL) {
			t.Skip("fixture filesystem rejects invalid UTF-8 filenames")
		}
		t.Fatal(err)
	}
	if _, err := PrepareWithLimits(ctx, root, source, "invalid-name", "scope", nil, NativeImportLimits()); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid path entered the import manifest", err)
	}
	must(t, os.Remove(invalid))
	w, err := PrepareWithLimits(ctx, root, source, "invalid-name", "scope", nil, NativeImportLimits())
	must(t, err)
	must(t, os.WriteFile(filepath.Join(w.Directory, name), []byte("new"), 0600))
	if _, err := ReviewWithPatchLimit(ctx, w, 8<<20); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid path entered the review inventory", err)
	}
}

func TestReviewKeepsByteHashesWhenDiffPatchIsNotUTF8(t *testing.T) {
	source, root := paths(t)
	ctx := context.Background()
	before := []byte("before\xff\n")
	after := []byte("after\xfe\n")
	must(t, os.WriteFile(filepath.Join(source, "encoded.txt"), before, 0600))
	w, err := PrepareWithLimits(ctx, root, source, "encoding", "scope", nil, NativeImportLimits())
	must(t, err)
	must(t, os.WriteFile(filepath.Join(w.Directory, "encoded.txt"), after, 0600))
	review, err := ReviewWithPatchLimit(ctx, w, 8<<20)
	must(t, err)
	want := FileChange{Path: "encoded.txt", Kind: "modified", BeforeSHA256: fmt.Sprintf("%x", sha256.Sum256(before)), AfterSHA256: fmt.Sprintf("%x", sha256.Sum256(after))}
	if !review.Changed || len(review.Files) != 1 || review.Files[0] != want {
		t.Fatal(review)
	}
	if review.Patch != "" || review.PatchUnavailable != "non_utf8" {
		t.Fatal("invalid patch text was not explicitly omitted", review)
	}
}

func TestTaskMountExcludesOtherCopiesAndBaseline(t *testing.T) {
	source, storage := paths(t)
	ctx := context.Background()
	a, err := PrepareWithLimits(ctx, storage, source, "a", "scope", nil, NativeImportLimits())
	must(t, err)
	b, err := PrepareWithLimits(ctx, storage, source, "b", "scope", nil, NativeImportLimits())
	must(t, err)
	main := filepath.Join(filepath.Dir(storage), "ordinary-main")
	must(t, os.Mkdir(main, 0700))
	must(t, os.WriteFile(filepath.Join(main, "notes.txt"), []byte("ordinary"), 0600))
	for _, scope := range []struct {
		root   string
		hidden []string
	}{
		{main, []string{a.Directory, b.Directory, a.Baseline, source}},
		{a.Directory, []string{main, b.Directory, a.Baseline, b.Baseline, source}},
	} {
		args := []string{"-c", `
			/bin/cat notes.txt >/dev/null || exit 7
			printf own > own.txt || exit 8
			for path in "$@"; do
			  if /bin/cat "$path/notes.txt" >/dev/null 2>&1; then exit 9; fi
			  if (printf escaped > "$path/escape.txt") 2>/dev/null; then exit 10; fi
			done`, "scope-test"}
		args = append(args, scope.hidden...)
		cmd, err := sandbox.Command(ctx, "/bin/bash", args, nil, scope.root, scope.root)
		must(t, err)
		cmd.Dir = scope.root
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		_, err = sandbox.Run(cmd, unix.SIGTERM)
		must(t, err)
		raw, err := os.ReadFile(filepath.Join(scope.root, "own.txt"))
		must(t, err)
		if string(raw) != "own" {
			t.Fatal("sandbox did not write within its own scope")
		}
	}
}

func TestImportLimitsRejectWholeTreeWithoutDroppingOwnerFiles(t *testing.T) {
	source, root := paths(t)
	must(t, os.WriteFile(filepath.Join(source, "extra"), []byte("retain owner file"), 0600))
	limits := NativeImportLimits()
	limits.Entries = 1
	if _, err := PrepareWithLimits(context.Background(), root, source, "small", "scope", nil, limits); !errors.Is(err, ErrInvalid) {
		t.Fatal("incomplete source became success", err)
	}
	if _, err := os.Stat(Location(root, "small").Directory); !os.IsNotExist(err) {
		t.Fatal("partial source published", err)
	}
	limits = NativeImportLimits()
	w, err := PrepareWithLimits(context.Background(), root, source, "complete", "scope", nil, limits)
	must(t, err)
	if data, err := os.ReadFile(filepath.Join(w.Directory, "extra")); err != nil || string(data) != "retain owner file" {
		t.Fatal("owner bytes omitted", string(data), err)
	}
}
