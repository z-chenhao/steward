package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/z-chenhao/assistant/internal/llm"
)

const credentialStoreHelp = "The credential files are unavailable. Check directory permissions and try again."

func (s *Server) startLogin(r *http.Request) error {
	// Only this provider implements account authorization today. The Web
	// chooser names it explicitly; retain empty for existing native clients.
	if provider := r.FormValue("provider"); provider != "" && provider != "chatgpt" {
		return errors.New("unsupported account provider")
	}
	if s.Accounts == nil {
		return llm.ErrNoBridge
	}
	s.mu.Lock()
	if s.auth.Status == "pending" {
		s.mu.Unlock()
		return llm.ErrConflict
	}
	lifetime := s.lifetime
	if lifetime == nil {
		lifetime = context.Background()
	}
	ctx, cancel := context.WithCancel(lifetime)
	s.authCancel = cancel
	s.auth = llm.AuthState{Status: "pending", Events: []llm.AuthEvent{}}
	s.authOnResources = accountReturn(r) == "/settings/models"
	s.authError = ""
	s.accountID = ""
	done := make(chan struct{})
	s.authDone = done
	s.mu.Unlock()
	accountID := r.FormValue("account")
	go func() {
		defer close(done)
		a, e := s.Accounts.Login(ctx, accountID, func(event llm.AuthEvent) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.auth.Events = append(s.auth.Events, event)
		})
		var selectionErr error
		if e == nil {
			selectionErr = s.selectAccountModelAfterLogin(ctx, a.ConnectionID)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.auth.Status = "connected"
		if selectionErr != nil {
			s.authError = "Signed in, but model activation failed. Choose a model in Chat."
		}
		if e != nil {
			s.auth.Status = "reauth_required"
			if errors.Is(e, llm.ErrSecretStore) || errors.Is(e, llm.ErrSecretCleanup) {
				s.authError = credentialStoreHelp
			}
		}
		s.accountID = a.ConnectionID
	}()

	return nil
}

func accountReturn(r *http.Request) string { return "/settings/models" }

func (s *Server) loadSignIn(p *page, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.authOnResources && path == "/settings/models") || (!s.authOnResources && path == "/settings/models/new") {
		p.Auth, p.AuthError, p.AccountID, p.AuthOnResources = s.auth, s.authError, s.accountID, s.authOnResources
	}
}
