package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
)

func TestMemoryExternalSecretsAndHostOwnerNeedNoCredentialFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, e := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if e != nil {
		t.Fatal(e)
	}
	cfg.Defaults = &config.Defaults{}
	cfg.App.OwnerID = "tenant-a:user-a"
	cfg.Memory = &memory.Config{EncryptionKeyRef: "opaque-managed-key"}
	secrets := secretmemory.New()
	if e = secrets.Put(ctx, "opaque-managed-key", []byte(strings.Repeat("a", 32))); e != nil {
		t.Fatal(e)
	}
	a, e := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.prepareCore(ctx); e != nil {
		a.Close()
		t.Fatal(e)
	}
	result, e := a.ManageMemory(ctx, "external-note", memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Verified lesson", Content: "Use a copy for import; draft only."})
	if e != nil {
		a.Close()
		t.Fatal(e)
	}
	ref, _ := result["ref"].(string)
	owner := a.store.OwnerID()
	diag, e := a.store.Diagnostics(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.memory.ReadDocument(ctx, recall.Scope{UserID: owner, AgentID: diag.AgentID}, 0, ref); e != nil {
		t.Fatal(e)
	}
	if _, e = a.memory.ReadDocument(ctx, recall.Scope{UserID: "local", AgentID: diag.AgentID}, 0, ref); e == nil {
		t.Fatal("implicit local principal accessed tenant")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"secrets", "account-secrets"} {
		if _, e = os.Stat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatal("unexpected local credential dependency", name, e)
		}
	}
	cfg.App.OwnerID = "tenant-b:user-b"
	if other, e := OpenRuntimeWithSecrets(ctx, cfg, secrets); e == nil {
		other.Close()
		t.Fatal("startup reassigned identity")
	}
}
