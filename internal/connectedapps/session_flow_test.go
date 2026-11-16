package connectedapps

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

func TestAppsSessionFlowHostRejectsForgedEffectsAndPreservesAcceptedLease(t *testing.T) {
	ctx := context.Background()
	prefix := []contract.SessionPlan{{Kind: "toolkit"}, {Kind: "auth"}, {Kind: "session"}, {Kind: "link"}}
	for _, fixture := range []struct {
		name       string
		after      int
		bad        contract.SessionPlan
		wantSource int
	}{
		{"TTL-before-source", 0, contract.SessionPlan{Kind: "toolkit", TTLSeconds: 1}, 0},
		{"public-without-proof", 1, contract.SessionPlan{Kind: "public"}, 1},
		{"skip-auth", 1, contract.SessionPlan{Kind: "session"}, 1},
		{"repeat-toolkit", 1, contract.SessionPlan{Kind: "toolkit"}, 1},
		{"repeat-auth", 2, contract.SessionPlan{Kind: "auth"}, 2},
		{"repeat-session", 3, contract.SessionPlan{Kind: "session"}, 3},
		{"repeat-link", 4, contract.SessionPlan{Kind: "link"}, 4},
		{"premature-pending", 3, contract.SessionPlan{Kind: "pending", TTLSeconds: 600}, 3},
		{"zero-lease", 4, contract.SessionPlan{Kind: "pending"}, 4},
		{"long-lease", 4, contract.SessionPlan{Kind: "pending", TTLSeconds: 601}, 4},
		{"overflow-lease", 4, contract.SessionPlan{Kind: "pending", TTLSeconds: math.MaxInt}, 4},
		{"negative-lease", 4, contract.SessionPlan{Kind: "pending", TTLSeconds: -1}, 4},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch r.URL.Path {
				case "/toolkits/example":
					_, _ = w.Write([]byte(`{"slug":"example","name":"Original"}`))
				case "/auth_configs":
					_, _ = w.Write([]byte(`{"items":[]}`))
				case "/tool_router/session":
					_, _ = w.Write([]byte(`{"session_id":"session_original"}`))
				case "/tool_router/session/session_original/link":
					_, _ = w.Write([]byte(`{"redirect_url":"https://connect.composio.dev/link/original","connected_account_id":"account_original"}`))
				default:
					t.Error("unexpected request", r.URL)
				}
			}))
			defer peer.Close()
			secrets := memory.New()
			_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-key"))
			store := &intentTestStore{state: State{KeyRef: "credential:fixture", UserID: "owner"}}
			s := &Service{Store: store, Secrets: secrets, Client: composio.Client{Endpoint: peer.URL}}
			index := 0
			p := ManagedSessionFlow(func(_ context.Context, revision int64, request contract.SessionRequest) (contract.SessionPlan, error) {
				if revision != 0 {
					t.Fatal("lost original CAS revision")
				}
				if index >= fixture.after {
					return fixture.bad, nil
				}
				plan := prefix[index]
				index++
				return plan, nil
			})
			if _, err := p.create(ctx, s, store.state, "example", "work", ""); !errors.Is(err, ErrUnavailable) || calls != fixture.wantSource || store.saves != 0 || len(s.pending) != 0 {
				t.Fatal("forged plan escaped source/lease/commit authority", calls, store.saves, err)
			}
		})
	}
	// Cache authorization is an accepted lease, independent of the current graph.
	// New shorter leases expire; old cache entries with no duration retain exactly
	// their original ten-minute interpretation, without a second session/link POST.
	store := &intentTestStore{state: State{Connections: []Connection{{ID: "new", Status: "pending"}, {ID: "old", Status: "pending"}}}}
	link := composio.Link{ConnectionID: "new", URL: "https://connect.composio.dev/link/original", AccountID: "account_original"}
	s := &Service{Store: store, pending: map[string]authorization{
		"new": {Link: link, Created: time.Now().Add(-2 * time.Minute), TTL: time.Minute},
		"old": {Link: link, Created: time.Now().Add(-2 * time.Minute)},
	}}
	if _, err := s.Continue(ctx, "new"); !errors.Is(err, ErrLinkExpired) {
		t.Fatal("short accepted lease extended", err)
	}
	if got, err := s.Continue(ctx, "old"); err != nil || got != link || store.saves != 0 {
		t.Fatal("old cache lost exact interpretation or replayed", got, err)
	}
	s.pending["old"] = authorization{Link: link, Created: time.Now().Add(-11 * time.Minute)}
	if _, err := s.Continue(ctx, "old"); !errors.Is(err, ErrLinkExpired) {
		t.Fatal("original maximum lease extended", err)
	}
}
