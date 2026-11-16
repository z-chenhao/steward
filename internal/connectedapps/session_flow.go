package connectedapps

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"time"
)

// SessionFlowPolicy is a private command reader, scoped to one captured graph.
// Candidate owns branching and effect sequence; Host owns immutable intent,
// original source references, no replay and atomic binding acceptance.
type SessionFlowPolicy struct {
	mode accountPolicyMode
	read func(context.Context, int64, contract.SessionRequest) (contract.SessionPlan, error)
}

func NativeSessionFlow() SessionFlowPolicy { return SessionFlowPolicy{mode: accountPolicyNative} }
func ManagedSessionFlow(read func(context.Context, int64, contract.SessionRequest) (contract.SessionPlan, error)) SessionFlowPolicy {
	return SessionFlowPolicy{mode: accountPolicyManaged, read: read}
}
func (p SessionFlowPolicy) available() bool {
	return p.mode == accountPolicyNative || p.mode == accountPolicyManaged && p.read != nil
}
func (p SessionFlowPolicy) create(ctx context.Context, s *Service, state State, toolkit, alias, explicitAuth string) (composio.Link, error) {
	if p.mode == accountPolicyNative {
		return s.connectNewLegacyV1(ctx, state, toolkit, alias, explicitAuth)
	}
	if !p.available() {
		return composio.Link{}, ErrUnavailable
	}
	client := s.client(state)
	request := contract.SessionRequest{Step: "begin"}
	var app composio.Toolkit
	var auth composio.AuthConfigSelection
	var session string
	var link composio.Link
	toolkitRead, authRead, sessionSent, linkSent := false, false, false, false
	// Each source operation is linear in this owner action; failures return
	// immediately, including uncertain POST outcomes. There is no recovery retry.
	for range 5 {
		plan, err := p.read(ctx, state.Revision, request)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return composio.Link{}, ErrConflict
			}
			return composio.Link{}, ErrUnavailable
		}
		if plan.Kind != "pending" && plan.TTLSeconds != 0 {
			return composio.Link{}, ErrUnavailable
		}
		switch plan.Kind {
		case "toolkit":
			if toolkitRead || authRead || sessionSent || linkSent {
				return composio.Link{}, ErrUnavailable
			}
			toolkitRead = true
			app, err = client.Toolkit(ctx, toolkit)
			if err != nil || app.Slug != toolkit {
				return composio.Link{}, ErrUnavailable
			}
			request.Step = "toolkit"
			request.NoAuth = app.NoAuth
		case "public":
			if !toolkitRead || !app.NoAuth || authRead || sessionSent || linkSent {
				return composio.Link{}, ErrUnavailable
			}
			return composio.Link{}, s.commitPublicConnection(ctx, state, toolkit, alias, app)
		case "auth":
			if !toolkitRead || app.NoAuth || authRead || sessionSent || linkSent {
				return composio.Link{}, ErrUnavailable
			}
			authRead = true
			auth, err = s.selectAuthConfig(ctx, state, client, toolkit, explicitAuth, app.ManagedAuth != nil && len(app.ManagedAuth) == 0)
			if err != nil {
				if errors.Is(err, ErrAuthConfigRequired) || errors.Is(err, ErrConflict) {
					return composio.Link{}, err
				}
				return composio.Link{}, ErrUnavailable
			}
			request.Step = "auth"
		case "session":
			if !toolkitRead || !authRead || sessionSent || linkSent {
				return composio.Link{}, ErrUnavailable
			}
			sessionSent = true
			session, err = client.CreateSession(ctx, state.UserID, toolkit, alias, auth.ID)
			if err != nil {
				return composio.Link{}, ErrUnavailable
			}
			request.Step = "session"
		case "link":
			if !sessionSent || session == "" || linkSent {
				return composio.Link{}, ErrUnavailable
			}
			linkSent = true
			link, err = client.CreateSessionLink(ctx, session, toolkit, alias)
			if err != nil {
				return composio.Link{}, ErrUnavailable
			}
			request.Step = "link"
		case "pending":
			ttl := time.Duration(plan.TTLSeconds) * time.Second
			if !linkSent || link.AccountID == "" || plan.TTLSeconds < 1 || plan.TTLSeconds > int(AuthorizationTTL/time.Second) {
				return composio.Link{}, ErrUnavailable
			}
			return s.commitPendingConnection(ctx, state, toolkit, alias, app, link, ttl)
		default:
			return composio.Link{}, ErrUnavailable
		}
	}
	return composio.Link{}, ErrUnavailable
}

func (s *Service) commitPublicConnection(ctx context.Context, state State, toolkit, alias string, app composio.Toolkit) error {
	// Called only after the original Toolkit identity/no_auth proof, never from a
	// candidate boolean. UUID and persisted binding shape remain owner mechanisms.
	state.Connections = append(state.Connections, Connection{ID: uuid.NewString(), Toolkit: toolkit, Name: app.Name, Alias: alias, Status: "active", Public: true})
	if err := s.save(ctx, state); err != nil {
		return err
	}
	return s.changed(ctx)
}
func (s *Service) commitPendingConnection(ctx context.Context, state State, toolkit, alias string, app composio.Toolkit, link composio.Link, ttl time.Duration) (composio.Link, error) {
	link.ConnectionID = uuid.NewString()
	link.ExpiresAt = time.Now().Add(ttl)
	state.Connections = append(state.Connections, Connection{ID: link.ConnectionID, Toolkit: toolkit, Name: app.Name, Alias: alias, AccountID: link.AccountID, Status: "pending"})
	if err := s.save(ctx, state); err != nil {
		return composio.Link{}, err
	}
	if s.pending == nil {
		s.pending = map[string]authorization{}
	}
	for id, p := range s.pending {
		if authorizationExpired(p) {
			delete(s.pending, id)
		}
	}
	s.pending[link.ConnectionID] = authorization{Link: link, Created: time.Now(), TTL: ttl}
	return link, nil
}
func authorizationExpired(p authorization) bool {
	ttl := p.TTL
	if ttl <= 0 || ttl > AuthorizationTTL {
		ttl = AuthorizationTTL
	}
	return time.Since(p.Created) >= ttl
}
