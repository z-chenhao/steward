package connectedapps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type intentTestStore struct {
	state State
	saves int
}

func (s *intentTestStore) LoadApps(context.Context) (State, error) { return s.state, nil }
func (s *intentTestStore) SaveApps(_ context.Context, state State, revision int64) error {
	if revision != s.state.Revision {
		return ErrConflict
	}
	s.state = state
	s.state.Revision++
	s.saves++
	return nil
}

func TestAppsConnectIntentScopedFactsAndOriginalPendingIdentity(t *testing.T) {
	state := State{Revision: 17, KeyRef: "credential:fixture", Connections: []Connection{{ID: "foreign", Toolkit: "other", Alias: "unrelated private", Status: "active"}, {ID: "older", Toolkit: "example", Name: "private original name", Alias: "older", AccountID: "private account id", Status: "pending"}, {ID: "", Toolkit: "example", Alias: "empty-id", Status: "pending"}}}
	calls := 0
	store := &intentTestStore{state: state}
	s := &Service{Store: store, ReadAccountPolicies: func(context.Context) (AccountPolicies, error) {
		return ManagedConnectIntent(func(_ context.Context, revision int64, request contract.ConnectRequest, rows []contract.ConnectRow) (contract.ConnectPlan, error) {
			calls++
			if revision != 17 || request.Count != 2 || request.Alias != "" || !reflect.DeepEqual(rows, []contract.ConnectRow{{Alias: "older", Facts: contract.Pending}, {Alias: "empty-id", Facts: contract.Pending}}) {
				t.Fatal("private group scope changed", revision, request, rows)
			}
			index := 1
			return contract.ConnectPlan{Kind: "pending", Row: &index}, nil
		}), nil
	}}
	intent, err := s.connectionIntent(context.Background(), state, "example", "")
	if err != nil || intent.PendingID == nil || *intent.PendingID != "" {
		t.Fatal("empty original ID inferred new connection", intent, err)
	}
	if _, err := s.Connect(context.Background(), "example", "", ""); !errors.Is(err, ErrLinkExpired) || calls != 2 || store.saves != 0 {
		t.Fatal("pending selection retried source or lost original cache semantics", calls, store.saves, err)
	}
}
func TestAppsConnectIntentRejectsForgedAliasReferenceAndManagedFallback(t *testing.T) {
	ref := func(i int) *int { return &i }
	state := State{KeyRef: "credential:fixture", Connections: []Connection{{Toolkit: "example", ID: "active", Alias: "active", Status: "active"}, {Toolkit: "example", ID: "pending", Alias: "work", Status: "pending"}}}
	for name, plan := range map[string]contract.ConnectPlan{
		"outside": {Kind: "pending", Row: ref(2)}, "negative": {Kind: "pending", Row: ref(-1)}, "non-pending": {Kind: "pending", Row: ref(0)},
		"rewritten-explicit": {Kind: "new", Alias: "changed"}, "duplicate-new": {Kind: "new", Alias: "work"}, "mixed": {Kind: "pending", Alias: "work", Row: ref(1)}, "unknown": {Kind: "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &Service{ReadAccountPolicies: func(context.Context) (AccountPolicies, error) {
				return ManagedConnectIntent(func(context.Context, int64, contract.ConnectRequest, []contract.ConnectRow) (contract.ConnectPlan, error) {
					return plan, nil
				}), nil
			}}
			if _, err := s.connectionIntent(context.Background(), state, "example", "work"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("forged identity or native interpretation escaped", plan, err)
			}
		})
	}
	for _, cause := range []error{errors.New("private diagnostics"), ErrConflict} {
		s := &Service{ReadAccountPolicies: func(context.Context) (AccountPolicies, error) { return AccountPolicies{}, cause }}
		_, err := s.connectionIntent(context.Background(), State{}, "example", "")
		want := ErrUnavailable
		if cause == ErrConflict {
			want = ErrConflict
		}
		if !errors.Is(err, want) {
			t.Fatal("error inferred native", err)
		}
	}
	s := &Service{ReadAccountPolicies: func(context.Context) (AccountPolicies, error) { return AccountPolicies{}, nil }}
	if _, err := s.connectionIntent(context.Background(), State{}, "example", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing managed plan inferred default", err)
	}
}

func TestAppsReconnectUnavailableIntentDoesNotCloseOriginalConnection(t *testing.T) {
	store := &intentTestStore{state: State{KeyRef: "credential:fixture", Connections: []Connection{{ID: "original", Toolkit: "example", Alias: "work", Status: "pending", Public: true}}}}
	s := &Service{Store: store, ReadAccountPolicies: func(context.Context) (AccountPolicies, error) { return AccountPolicies{}, nil }}
	if _, err := s.Reconnect(context.Background(), "original"); !errors.Is(err, ErrUnavailable) || store.saves != 0 || store.state.Connections[0].Status != "pending" {
		t.Fatal("unavailable policy closed original connection", store.state, store.saves, err)
	}
	calls := 0
	reads := 0
	s.ReadAccountPolicies = func(context.Context) (AccountPolicies, error) {
		calls++
		return ManagedConnectIntent(func(_ context.Context, revision int64, request contract.ConnectRequest, rows []contract.ConnectRow) (contract.ConnectPlan, error) {
			reads++
			if revision != 2 || len(rows) != 1 || rows[0].Facts&contract.Disconnected == 0 {
				t.Fatal("reconnect policy used pre-revocation state", revision, rows)
			}
			return contract.ConnectPlan{Kind: "new", Alias: request.Alias}, nil
		}).WithSessionFlow(NativeSessionFlow()).WithAccountClose(NativeAccountClose()).WithReconnect(NativeReconnect()), nil
	}
	// The original native source step fails without credentials. Its already
	// accepted local closure remains; no second policy graph is selected.
	if _, err := s.Reconnect(context.Background(), "original"); !errors.Is(err, ErrUnavailable) || calls != 1 || reads != 1 || store.saves != 2 {
		t.Fatal("reconnect reselected policy or changed native closure", calls, reads, store.saves, err)
	}
}
