// Package account owns Steward's independent, trusted account lifecycle.
package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/llm"
)

type Store interface {
	Auth(context.Context, string) (llm.AuthConnection, error)
	Accounts(context.Context) ([]llm.AuthConnection, error)
	DeviceID(context.Context) (string, error)
	Registration(context.Context, string) (llm.AccountRegistration, error)
	InsertAccount(context.Context, llm.AuthConnection, llm.AccountRegistration) error
	AccountState(context.Context, llm.AuthConnection, string, *llm.OAuthCredential) error
}
type Provider interface {
	AccountStart(context.Context, string, *llm.AccountRegistration, *llm.OAuthCredential) (llm.AccountLogin, error)
	AccountPoll(context.Context) (llm.AccountLogin, error)
	AccountCancel(context.Context) error
	AccountRefresh(context.Context, llm.OAuthCredential) (llm.OAuthCredential, error)
	AccountLogout(context.Context, string, *llm.OAuthCredential) (bool, error)
}
type Manager struct {
	Store    Store
	Secrets  llm.SecretStore
	Provider Provider
	LockRoot string
	login    sync.Mutex
}

// Recover reconciles interrupted auth mutations only after acquiring the same
// cross-process lock. A live refresh/logout owner cannot be mistaken for a crash.
func (m *Manager) Recover(ctx context.Context) error {
	accounts, err := m.Store.Accounts(ctx)
	if err != nil {
		return err
	}
	files, checkExists := m.Secrets.(interface {
		Exists(context.Context, string) (bool, error)
	})
	for _, a := range accounts {
		if a.Status == "connected" && !checkExists {
			continue
		}
		err = m.locked(ctx, a.ConnectionID, func() error {
			current, err := m.Store.Auth(ctx, a.ConnectionID)
			if err != nil {
				return err
			}
			if current.Status == "connected" && checkExists {
				exists, err := files.Exists(ctx, current.CredentialRef)
				if err == nil && !exists {
					return m.Store.AccountState(ctx, current, "reauth_required", nil)
				}
				return nil
			}
			if current.Status == "refreshing" {
				err = m.lose(current)
				if errors.Is(err, llm.ErrSecretCleanup) || errors.Is(err, llm.ErrStorage) {
					return err
				}
				return nil
			}
			if current.Status == "reauth_required" || current.Status == "logged_out" {
				if m.Secrets.Delete(ctx, current.CredentialRef) != nil {
					return llm.ErrSecretCleanup
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Advisory locks are held across the network + SecretStore + metadata commit.
// Kernel release on process death makes a persisted refreshing row recognizable
// as interrupted once the next owner obtains the lock. No rotating call retries.
func (m *Manager) locked(ctx context.Context, id string, fn func() error) error {
	if _, err := uuid.Parse(id); err != nil {
		return llm.AuthRequired
	}
	if err := os.MkdirAll(m.LockRoot, 0700); err != nil {
		return llm.ErrStorage
	}
	f, err := os.OpenFile(filepath.Join(m.LockRoot, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return llm.ErrStorage
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return llm.ErrStorage
		}
		select {
		case <-ctx.Done():
			return llm.ProviderUnavailable
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if ctx.Err() != nil {
		return llm.ProviderUnavailable
	}
	return fn()
}
func (m *Manager) read(ctx context.Context, a llm.AuthConnection) (llm.OAuthCredential, error) {
	var c llm.OAuthCredential
	data, err := m.Secrets.Get(ctx, a.CredentialRef)
	if err != nil {
		return c, llm.AuthRequired
	}
	defer clear(data)
	if json.Unmarshal(data, &c) != nil || c.ClientID == "" || c.SubjectHash == "" || c.AccessToken == "" || c.RefreshToken == "" || strings.HasPrefix(c.AccessToken, "sk-") {
		return c, llm.AuthRequired
	}
	r, err := m.Store.Registration(ctx, a.ConnectionID)
	if err != nil || r.ClientID != c.ClientID || r.SubjectHash != c.SubjectHash {
		return llm.OAuthCredential{}, llm.AuthRequired
	}
	return c, nil
}
func (m *Manager) put(ctx context.Context, a llm.AuthConnection, c llm.OAuthCredential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return llm.ErrSecretStore
	}
	defer clear(data)
	if m.Secrets.Put(ctx, a.CredentialRef, data) != nil {
		return llm.ErrSecretStore
	}
	return nil
}
func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func (m *Manager) Login(ctx context.Context, id string, emit func(llm.AuthEvent)) (llm.AuthConnection, error) {
	if !m.login.TryLock() {
		return llm.AuthConnection{}, llm.ErrConflict
	}
	defer m.login.Unlock()
	if m.Provider == nil {
		return llm.AuthConnection{}, llm.ErrNoBridge
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var previous llm.AuthConnection
	var registration *llm.AccountRegistration
	var credential *llm.OAuthCredential
	if id != "" {
		var err error
		previous, err = m.Store.Auth(ctx, id)
		if err != nil || previous.AuthKind != "chatgpt_oauth" {
			return previous, llm.AuthRequired
		}
		r, err := m.Store.Registration(ctx, id)
		if err != nil {
			return previous, err
		}
		registration = &r
		if c, err := m.read(ctx, previous); err == nil {
			credential = &c
		}
	}
	device, err := m.Store.DeviceID(ctx)
	if err != nil {
		return llm.AuthConnection{}, err
	}
	result, err := m.Provider.AccountStart(ctx, device, registration, credential)
	defer func() { cleanup, stop := cleanupContext(); defer stop(); _ = m.Provider.AccountCancel(cleanup) }()
	for err == nil && result.Status == "pending" {
		for _, event := range result.Events {
			if emit != nil {
				emit(event)
			}
		}
		select {
		case <-ctx.Done():
			return llm.AuthConnection{}, llm.GenerationCancelled
		case <-time.After(50 * time.Millisecond):
		}
		result, err = m.Provider.AccountPoll(ctx)
	}
	if err != nil || result.Status != "connected" || result.Credential == nil {
		return llm.AuthConnection{}, llm.AuthRequired
	}
	c := *result.Credential
	if c.ClientID == "" || len(c.SubjectHash) != 64 || c.AccessToken == "" || c.RefreshToken == "" || !c.ExpiresAt.After(time.Now()) {
		return llm.AuthConnection{}, llm.AuthRequired
	}
	if id == "" {
		u, err := uuid.NewV7()
		if err != nil {
			return llm.AuthConnection{}, llm.ErrStorage
		}
		id = u.String()
		now := time.Now().UTC().Truncate(time.Microsecond)
		h := sha256.Sum256([]byte(c.ClientID))
		previous = llm.AuthConnection{AuthInput: llm.AuthInput{Provider: "openai", AuthKind: "chatgpt_oauth", AccountLabel: "Account " + c.SubjectHash[:8], WorkspaceLabel: "Workspace " + hex.EncodeToString(h[:4]), ExpiresAt: &c.ExpiresAt}, ConnectionID: id, CredentialRef: "credential:" + id, Status: "connected", CreatedAt: now, UpdatedAt: now, Version: 1}
		err = m.locked(ctx, id, func() error {
			if err := m.put(ctx, previous, c); err != nil {
				return err
			}
			if err := m.Store.InsertAccount(ctx, previous, llm.AccountRegistration{ClientID: c.ClientID, SubjectHash: c.SubjectHash}); err != nil {
				cleanup, stop := cleanupContext()
				defer stop()
				if m.Secrets.Delete(cleanup, previous.CredentialRef) != nil {
					return errors.Join(err, llm.ErrSecretCleanup)
				}
				return err
			}
			return nil
		})
		return previous, err
	}
	err = m.locked(ctx, id, func() error {
		a, err := m.Store.Auth(ctx, id)
		if err != nil {
			return err
		}
		if a.Version != previous.Version {
			return llm.ErrConflict
		}
		if registration.ClientID != c.ClientID || registration.SubjectHash != c.SubjectHash {
			return llm.AuthRequired
		}
		// Close admission before replacing a credential; failure/restart cannot use
		// a partially committed replacement. Reauthorization revokes old proof.
		if a.Status != "reauth_required" && a.Status != "logged_out" {
			if err = m.Store.AccountState(ctx, a, "reauth_required", nil); err != nil {
				return err
			}
			a, err = m.Store.Auth(ctx, id)
			if err != nil {
				return err
			}
		}
		if err = m.put(ctx, a, c); err != nil {
			return err
		}
		if err = m.Store.AccountState(ctx, a, "connected", &c); err != nil {
			cleanup, stop := cleanupContext()
			defer stop()
			if m.Secrets.Delete(cleanup, a.CredentialRef) != nil {
				return errors.Join(err, llm.ErrSecretCleanup)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return llm.AuthConnection{}, err
	}
	return m.Store.Auth(ctx, id)
}

func (m *Manager) Ensure(ctx context.Context, id string) (llm.AuthConnection, llm.OAuthCredential, error) {
	var a llm.AuthConnection
	var c llm.OAuthCredential
	err := m.locked(ctx, id, func() error {
		var err error
		a, err = m.Store.Auth(ctx, id)
		if err != nil || a.AuthKind != "chatgpt_oauth" {
			return llm.AuthRequired
		}
		if a.Status == "refreshing" {
			return m.lose(a)
		} // Previous owner died; never retry a rotating token.
		if a.Status != "connected" {
			return llm.AuthRequired
		}
		c, err = m.read(ctx, a)
		if err != nil {
			// A caller can disappear during a local read, before token rotation.
			if ctx.Err() != nil {
				return llm.ProviderUnavailable
			}
			return m.lose(a)
		}
		if c.ExpiresAt.After(time.Now().Add(3 * time.Minute)) {
			return nil
		}
		if m.Provider == nil {
			return llm.ErrNoBridge
		}
		if err = m.Store.AccountState(ctx, a, "refreshing", nil); err != nil {
			return err
		}
		a, err = m.Store.Auth(ctx, id)
		if err != nil {
			return err
		}
		refreshCtx, cancelRefresh := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		updated, err := m.Provider.AccountRefresh(refreshCtx, c)
		cancelRefresh()
		if err != nil {
			if errors.Is(err, llm.AuthRequired) {
				return m.lose(a)
			}
			// Transient network/5xx errors or timeouts must not destroy stored OAuth credentials.
			_ = m.Store.AccountState(context.WithoutCancel(ctx), a, "connected", nil)
			return err
		}
		if updated.ClientID != c.ClientID || updated.SubjectHash != c.SubjectHash || !updated.ExpiresAt.After(time.Now().Add(3*time.Minute)) {
			return m.lose(a)
		}
		if err = m.put(ctx, a, updated); err != nil {
			_ = m.lose(a)
			return err
		}
		if err = m.Store.AccountState(ctx, a, "connected", &updated); err != nil {
			_ = m.lose(a)
			return err
		}
		c = updated
		a, err = m.Store.Auth(ctx, id)
		return err
	})
	if err != nil {
		return llm.AuthConnection{}, llm.OAuthCredential{}, err
	}
	return a, c, nil
}
func (m *Manager) lose(a llm.AuthConnection) error {
	ctx, stop := cleanupContext()
	defer stop()
	err := m.Store.AccountState(ctx, a, "reauth_required", nil)
	if m.Secrets.Delete(ctx, a.CredentialRef) != nil {
		return errors.Join(llm.AuthRequired, err, llm.ErrSecretCleanup)
	}
	return errors.Join(llm.AuthRequired, err)
}
func (m *Manager) Lose(ctx context.Context, id string) error {
	return m.LoseVersion(ctx, id, 0)
}
func (m *Manager) LoseVersion(ctx context.Context, id string, version int64) error {
	return m.locked(ctx, id, func() error {
		a, err := m.Store.Auth(ctx, id)
		if err != nil {
			return err
		}
		if version != 0 && a.Version != version {
			return nil
		}
		if a.Status == "reauth_required" || a.Status == "logged_out" {
			return nil
		}
		return m.lose(a)
	})
}
func (m *Manager) Logout(ctx context.Context, id string) (bool, error) {
	confirmed := false
	err := m.locked(ctx, id, func() error {
		a, err := m.Store.Auth(ctx, id)
		if err != nil || a.AuthKind != "chatgpt_oauth" {
			return llm.AuthRequired
		}
		c, _ := m.read(ctx, a)
		if a.Status != "logged_out" {
			if err = m.Store.AccountState(ctx, a, "logged_out", nil); err != nil {
				return err
			}
		}
		cleanup, stop := cleanupContext()
		defer stop()
		if m.Provider != nil {
			confirmed, _ = m.Provider.AccountLogout(cleanup, id, &c)
		}
		if m.Secrets.Delete(cleanup, a.CredentialRef) != nil {
			return llm.ErrSecretCleanup
		}
		return nil
	})
	return confirmed, err
}
