package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftTreeMatchesGitObjectsAndModes(t *testing.T) {
	ctx := context.Background()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(parent, "work")
	if err := os.MkdirAll(filepath.Join(work, "a", "b"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"a-x": []byte("data"), "a/b/run": []byte("#!/bin/sh\n"), "a/file": {0, 255}, "a.B": {}}
	for name, data := range files {
		mode := os.FileMode(0600)
		if name == "a/b/run" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(work, name), data, mode); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := CaptureCandidate(ctx, Workspace{Directory: work, Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	tree, err := DraftObjects(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = work
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatal(args, string(out), e)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("-c", "core.fileMode=true", "add", "--all")
	if want := git("write-tree"); tree.SHA != want || len(tree.Entries) != len(files) {
		t.Fatalf("candidate Git tree %s; git wrote %s; entries %+v", tree.SHA, want, tree.Entries)
	}
	for _, entry := range tree.Entries {
		if want := git("hash-object", "--no-filters", "--", entry.Path); entry.SHA != want {
			t.Fatalf("%s = %s, want %s", entry.Path, entry.SHA, want)
		}
	}
	if err := os.WriteFile(filepath.Join(snapshot.Directory, "a-x"), []byte("later mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = DraftObjects(ctx, snapshot); err == nil {
		t.Fatal("changed private candidate received Git objects")
	}
}
