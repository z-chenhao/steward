package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
)

func TestRecallSnapshotVaultPinsKeyWithoutEnablingMemory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	secrets := secretmemory.New()
	first, err := RecallSnapshotVault(ctx, root, secrets, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := first.Seal(ctx, "private authorized candidates", "snapshot:run:read")
	if err != nil {
		t.Fatal(err)
	}
	if err = secrets.Put(ctx, "credential:later-key", []byte(strings.Repeat("n", 32))); err != nil {
		t.Fatal(err)
	}
	pinned, err := RecallSnapshotVault(ctx, root, secrets, "credential:later-key", first.Ref)
	if err != nil || pinned.Ref != first.Ref {
		t.Fatal("configured key rebound old snapshot", err)
	}
	if plain, err := pinned.Open(ctx, cipher, "snapshot:run:read"); err != nil || plain != "private authorized candidates" {
		t.Fatal(plain, err)
	}
	original, err := os.ReadFile(filepath.Join(root, "default-memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = secrets.Delete(ctx, first.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err = RecallSnapshotVault(ctx, root, secrets, "credential:later-key", first.Ref); err == nil {
		t.Fatal("lost snapshot key silently replaced")
	}
	after, err := os.ReadFile(filepath.Join(root, "default-memory.json"))
	if err != nil || string(after) != string(original) {
		t.Fatal("missing key changed published identity", err)
	}
	if _, err = os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("snapshot encryption enabled/rewrote capability configuration")
	}
}
