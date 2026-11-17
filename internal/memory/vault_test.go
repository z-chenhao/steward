package memory

import (
	"bytes"
	"context"
	secrets "github.com/z-chenhao/assistant/internal/llm/memory"
	"testing"
)

func TestVaultScopeTamperingAndKeyLossFailClosed(t *testing.T) {
	ctx := context.Background()
	s := secrets.New()
	if e := s.Put(ctx, "key", bytes.Repeat([]byte{3}, 32)); e != nil {
		t.Fatal(e)
	}
	v := Vault{Secrets: s, Ref: "key"}
	sealed, e := v.Seal(ctx, "sensitive record", "alice:record")
	if e != nil {
		t.Fatal(e)
	}
	if plain, e := v.Open(ctx, sealed, "alice:record"); e != nil || plain != "sensitive record" {
		t.Fatal(plain, e)
	}
	if _, e = v.Open(ctx, sealed, "bob:record"); e == nil {
		t.Fatal("cross-scope ciphertext accepted")
	}
	if _, e = v.Open(ctx, sealed+"x", "alice:record"); e == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if e = s.Delete(ctx, "key"); e != nil {
		t.Fatal(e)
	}
	if _, e = v.Open(ctx, sealed, "alice:record"); e == nil {
		t.Fatal("missing key accepted")
	}
}
func TestExploratoryAndTransientReportsAreNotStable(t *testing.T) {
	if Stable("I might explore independent development") || Stable("I felt tired today") {
		t.Fatal("exploratory/transient report considered stable")
	}
}
