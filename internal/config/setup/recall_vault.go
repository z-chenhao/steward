package setup

import (
	"context"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
)

// RecallSnapshotVault protects staged retrieval evidence without enabling
// Memory maintenance or changing its configuration. Existing snapshot references
// resolve exactly; a missing key is never replaced to decrypt an old record.
func RecallSnapshotVault(ctx context.Context, root string, secrets llm.SecretStore, configuredRef, snapshotRef string) (memory.Vault, error) {
	ref := snapshotRef
	if ref == "" {
		ref = configuredRef
	}
	if ref == "" {
		var err error
		ref, err = defaultMemoryKey(ctx, root, secrets)
		if err != nil {
			return memory.Vault{}, err
		}
	}
	v := memory.Vault{Secrets: secrets, Ref: ref}
	if _, err := v.Digest(ctx, "recall snapshot key identity"); err != nil {
		return memory.Vault{}, err
	}
	return v, nil
}
