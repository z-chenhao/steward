package coding

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodingProjectEvidenceKeepsCapturedBytesAfterCandidateDeletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "assertions.json"), []byte(`{"actual":"persisted"}`), 0600); err != nil {
		t.Fatal(err)
	}
	items, streams, failures, err := CaptureEvidence(context.Background(), dir, []string{"assertions.json"})
	if err != nil || len(items) != 1 || len(streams) != 1 || len(failures) != 0 || items[0].ArtifactIndex != 0 || len(items[0].SHA256) != 64 || items[0].Bytes != 22 {
		t.Fatal(items, streams, failures, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(streams[0].Reader)
	if err != nil || string(raw) != `{"actual":"persisted"}` {
		t.Fatal("artifact stream depended on deleted candidate", string(raw), err)
	}
}

func TestCodingProjectEvidenceRejectsEscapeSpecialFilesAndOversizedResults(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("must-not-read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, "large"))
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate((8 << 20) + 1)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	items, streams, failures, err := CaptureEvidence(context.Background(), dir, []string{"missing", "link", "directory", "large"})
	if err != nil || len(items) != 0 || len(streams) != 0 || len(failures) != 4 {
		t.Fatal(items, streams, failures, err)
	}
	if failures[0].ErrorCode != "NOT_FOUND" || failures[1].ErrorCode != "INVALID_ARGUMENT" || failures[2].ErrorCode != "INVALID_ARGUMENT" || failures[3].ErrorCode != "RESOURCE_LIMIT" {
		t.Fatal(failures)
	}
	for _, names := range [][]string{{"../private"}, {"/etc/passwd"}, {"a", "a"}, {"."}, {"a\\b"}, {strings.Repeat("x", 1025)}} {
		if _, _, _, err := CaptureEvidence(context.Background(), dir, names); err != ErrInvalid {
			t.Fatal("invalid evidence plan accepted", names, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := CaptureEvidence(ctx, dir, []string{"missing"}); err != context.Canceled {
		t.Fatal("cancelled capture continued", err)
	}
}
