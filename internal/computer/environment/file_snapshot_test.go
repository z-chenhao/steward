package environment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSnapshotReadsOriginalBytesWithoutMutation(t *testing.T) {
	l := localFixture(t)
	data := []byte("原文\r\n\x00\xff binary evidence\n")
	path := filepath.Join(l.Config.Root, "evidence.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	value, err := l.SnapshotFile(context.Background(), "evidence.bin")
	if err != nil || !value.Exists || !bytes.Equal(value.Data, data) || value.Hash != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatal("snapshot changed bytes or identity", value, err)
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(after, data) {
		t.Fatal("read mutated file", e)
	}
	for _, name := range []string{"missing", "missing-parent/file"} {
		value, err = l.SnapshotFile(context.Background(), name)
		if err != nil || value.Exists || len(value.Data) != 0 || value.Hash != "" {
			t.Fatal("missing file invented evidence", name, value, err)
		}
	}
	if err := os.WriteFile(filepath.Join(l.Config.Root, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	value, err = l.SnapshotFile(context.Background(), "empty")
	if err != nil || !value.Exists || len(value.Data) != 0 || value.Hash != fmt.Sprintf("%x", sha256.Sum256(nil)) {
		t.Fatal("empty and absent files conflated", value, err)
	}
}

func TestFileSnapshotPreservesWorkspaceAndRegularFileBoundaries(t *testing.T) {
	l := localFixture(t)
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("not authorized"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(l.Config.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(l.Config.Root, "hard")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{outside, "../private", "link", "hard", "dir/../private", "bad\x00name"} {
		value, err := l.SnapshotFile(context.Background(), name)
		if err == nil || err.Code != "PERMISSION_DENIED" || value.Exists || len(value.Data) != 0 {
			t.Fatal("snapshot escaped file scope", name, value, err)
		}
	}
	path := filepath.Join(l.Config.Root, "large")
	data := bytes.Repeat([]byte("x"), 8<<20)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	value, err := l.SnapshotFile(context.Background(), "large")
	if err != nil || !bytes.Equal(value.Data, data) {
		t.Fatal("legal complete file truncated", len(value.Data), err)
	}
	file, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	_, e = file.Write([]byte("x"))
	closeErr := file.Close()
	if e != nil || closeErr != nil {
		t.Fatal(e, closeErr)
	}
	value, err = l.SnapshotFile(context.Background(), "large")
	if err == nil || err.Code != "PERMISSION_DENIED" || len(value.Data) != 0 {
		t.Fatal("oversized file partially succeeded", len(value.Data), err)
	}
}
