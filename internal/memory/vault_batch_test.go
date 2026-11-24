package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/recall"
)

type countingSecretStore struct {
	llm.SecretStore
	key   []byte
	reads int
}

func (s *countingSecretStore) Get(context.Context, string) ([]byte, error) {
	s.reads++
	return append([]byte(nil), s.key...), nil
}

func TestVaultOpenBatchReadsSecretOnceAndEnforcesAAD(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secrets := &countingSecretStore{key: key}
	v := Vault{Secrets: secrets, Ref: "mem-key"}
	ctx := context.Background()

	s1, err := v.Seal(ctx, "secret one", "record:1")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := v.Seal(ctx, "secret two", "record:2")
	if err != nil {
		t.Fatal(err)
	}
	secrets.reads = 0

	open, err := v.OpenBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := open(s1, "record:1")
	if err != nil || p1 != "secret one" {
		t.Fatalf("open(s1) = %q, %v", p1, err)
	}
	p2, err := open(s2, "record:2")
	if err != nil || p2 != "secret two" {
		t.Fatalf("open(s2) = %q, %v", p2, err)
	}
	if secrets.reads != 1 {
		t.Fatalf("SecretStore.Get called %d times, want 1", secrets.reads)
	}
	if _, err := open(s1, "record:2"); !errors.Is(err, recall.ErrUnavailable) {
		t.Fatalf("mismatched AAD returned %v, want ErrUnavailable", err)
	}
}
