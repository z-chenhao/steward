package account

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/llm"
)

type cancelledReadStore struct {
	Store
	updated bool
}

func (s *cancelledReadStore) Auth(_ context.Context, id string) (llm.AuthConnection, error) {
	return llm.AuthConnection{
		AuthInput:    llm.AuthInput{AuthKind: "chatgpt_oauth"},
		ConnectionID: id, Status: "connected", Version: 1,
	}, nil
}

func (s *cancelledReadStore) AccountState(context.Context, llm.AuthConnection, string, *llm.OAuthCredential) error {
	s.updated = true
	return nil
}

type cancelledSecretRead struct {
	llm.SecretStore
	cancel  context.CancelFunc
	deleted bool
}

func (s *cancelledSecretRead) Get(context.Context, string) ([]byte, error) {
	s.cancel()
	return nil, llm.ErrSecretStore
}

func (s *cancelledSecretRead) Delete(context.Context, string) error {
	s.deleted = true
	return nil
}

func TestEnsureDoesNotRevokeAccountWhenCredentialReadIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &cancelledReadStore{}
	secrets := &cancelledSecretRead{cancel: cancel}
	manager := Manager{Store: store, Secrets: secrets, LockRoot: t.TempDir()}

	_, _, err := manager.Ensure(ctx, uuid.NewString())
	if !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatalf("Ensure returned %v, want temporary unavailability", err)
	}
	if store.updated || secrets.deleted {
		t.Fatal("cancelled credential read changed account state or deleted the credential")
	}
}

type refreshTestStore struct {
	Store
	status string
}

func (s *refreshTestStore) Auth(_ context.Context, id string) (llm.AuthConnection, error) {
	status := s.status
	if status == "" {
		status = "connected"
	}
	return llm.AuthConnection{
		AuthInput:     llm.AuthInput{AuthKind: "chatgpt_oauth"},
		ConnectionID:  id,
		CredentialRef: "oauth-ref",
		Status:        status,
		Version:       1,
	}, nil
}

func (s *refreshTestStore) AccountState(_ context.Context, _ llm.AuthConnection, status string, _ *llm.OAuthCredential) error {
	s.status = status
	return nil
}

func (s *refreshTestStore) Registration(_ context.Context, _ string) (llm.AccountRegistration, error) {
	return llm.AccountRegistration{ClientID: "client-1", SubjectHash: "sub-1", AuthEpoch: 1}, nil
}

type staticSecretStore struct {
	llm.SecretStore
	raw     []byte
	deleted bool
}

func (s *staticSecretStore) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.raw...), nil
}

func (s *staticSecretStore) Delete(context.Context, string) error {
	s.deleted = true
	return nil
}

type fakeRefreshProvider struct {
	Provider
	err error
}

func (p fakeRefreshProvider) AccountRefresh(context.Context, llm.OAuthCredential) (llm.OAuthCredential, error) {
	return llm.OAuthCredential{}, p.err
}

func TestEnsurePreservesCredentialOnTransientRefreshFailure(t *testing.T) {
	cred, _ := json.Marshal(llm.OAuthCredential{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ClientID:     "client-1",
		SubjectHash:  "sub-1",
		ExpiresAt:    time.Now().Add(30 * time.Second),
	})
	store := &refreshTestStore{}
	secrets := &staticSecretStore{raw: cred}
	manager := Manager{
		Store:    store,
		Secrets:  secrets,
		Provider: fakeRefreshProvider{err: llm.ProviderUnavailable},
		LockRoot: t.TempDir(),
	}
	_, _, err := manager.Ensure(context.Background(), uuid.NewString())
	if !errors.Is(err, llm.ProviderUnavailable) {
		t.Fatalf("Ensure returned %v, want ProviderUnavailable", err)
	}
	if secrets.deleted {
		t.Fatal("transient refresh failure must not delete OAuth credentials")
	}
	if store.status != "connected" {
		t.Fatalf("account status = %q, want connected", store.status)
	}

	// Permanent auth rejection still revokes credentials and requires reauth.
	manager.Provider = fakeRefreshProvider{err: llm.AuthRequired}
	_, _, err = manager.Ensure(context.Background(), uuid.NewString())
	if !errors.Is(err, llm.AuthRequired) {
		t.Fatalf("Ensure returned %v, want AuthRequired", err)
	}
	if !secrets.deleted || store.status != "reauth_required" {
		t.Fatalf("permanent auth error did not revoke credential (deleted=%v status=%q)", secrets.deleted, store.status)
	}
}
