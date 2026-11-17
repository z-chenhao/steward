package environment

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileChangeUsesChosenByteSpanButRetainsCASAndExactReplacement(t *testing.T) {
	l := localFixture(t)
	ctx := context.Background()
	path := filepath.Join(l.Config.Root, "file")
	original := "前缀\nOLD\nuser dirty tail\n"
	if err := os.WriteFile(path, []byte(original), 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, e := l.SnapshotFile(ctx, "file")
	if e != nil {
		t.Fatal(e)
	}
	change := FileChange{Operation: "edit", Path: "file", BeforeHash: &snapshot.Hash, OldText: "OLD", NewText: "NEW", Start: len("前缀\n")}
	wrong := change
	wrong.Start++
	if r := l.ApplyFile(ctx, wrong); r.Error == nil || r.Error.Code != "INVALID_ARGUMENT" {
		t.Fatal("unverified span changed a file", r)
	}
	if r := l.ApplyFile(ctx, change); r.Error != nil {
		t.Fatal(r)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "前缀\nNEW\nuser dirty tail\n" {
		t.Fatal("exact delta or unrelated dirty bytes changed", string(raw), err)
	}
	if mode, err := os.Stat(path); err != nil || mode.Mode().Perm() != 0700 {
		t.Fatal("mode changed", mode, err)
	}
	if r := l.ApplyFile(ctx, change); r.Error == nil || r.Error.Code != "PERMISSION_DENIED" {
		t.Fatal("stale accepted preimage replayed", r)
	}
	latest, e := l.SnapshotFile(ctx, "file")
	if e != nil {
		t.Fatal(e)
	}
	change.BeforeHash = &latest.Hash
	change.OldText = "NEW"
	change.NewText = ""
	if r := l.ApplyFile(ctx, change); r.Error != nil {
		t.Fatal("empty replacement rejected", r)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "前缀\n\nuser dirty tail\n" {
		t.Fatal(string(raw), err)
	}
}

func TestFileChangeCreatesEmptyFilesAndRefusesBlindOverwriteAndAmbiguousEdit(t *testing.T) {
	l := localFixture(t)
	ctx := context.Background()
	change := FileChange{Operation: "write", Path: "nested/empty", Content: ""}
	if r := l.ApplyFile(ctx, change); r.Error != nil {
		t.Fatal("empty create rejected", r)
	}
	change.Content = "overwrite"
	if r := l.ApplyFile(ctx, change); r.Error == nil || r.Error.Code != "PERMISSION_DENIED" {
		t.Fatal("blind overwrite succeeded", r)
	}
	if err := os.WriteFile(filepath.Join(l.Config.Root, "ambiguous"), []byte("same and same"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, e := l.SnapshotFile(ctx, "ambiguous")
	if e != nil {
		t.Fatal(e)
	}
	edit := FileChange{Operation: "edit", Path: "ambiguous", BeforeHash: &snapshot.Hash, OldText: "same", NewText: "wrong", Start: 0}
	if r := l.ApplyFile(ctx, edit); r.Error == nil || r.Error.Code != "INVALID_ARGUMENT" {
		t.Fatal("ambiguous edit accepted", r)
	}
	if data, err := os.ReadFile(filepath.Join(l.Config.Root, "ambiguous")); err != nil || string(data) != "same and same" {
		t.Fatal("rejected plan mutated bytes", string(data), err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(l.Config.Root, ".steward-write-*")); len(leftovers) != 0 {
		t.Fatal("staging leaked", leftovers)
	}
}
