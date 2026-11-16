package connectedapps

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

type closeGoldenCase struct {
	Name                                                                                                    string
	Public, Disabled, WrongID, WrongUser, WrongToolkit, InvalidAccount, AlreadyClosed, Missing, ChangedFail bool
	AccountStatus, RevokeStatus, DeleteStatus, FailSave                                                     int
	RevokeBody                                                                                              string
	Expected                                                                                                struct {
		Error, Status               string
		Confirmed, Cached           bool
		Revision                    int64
		Saves, Changed              int
		Requests                    []string
		SecondError                 string
		SecondRequests, SecondSaves int
	}
}
type closeGoldenStore struct {
	state          State
	writes, failAt int
}

func (s *closeGoldenStore) LoadApps(context.Context) (State, error) {
	v := s.state
	v.Connections = append([]Connection(nil), v.Connections...)
	return v, nil
}
func (s *closeGoldenStore) SaveApps(_ context.Context, v State, revision int64) error {
	s.writes++
	if s.writes == s.failAt || revision != s.state.Revision {
		return ErrConflict
	}
	v.Connections = append([]Connection(nil), v.Connections...)
	s.state = v
	s.state.Revision++
	return nil
}
func closeGoldenError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrConflict):
		return "conflict"
	}
	return "callback"
}
func TestAppsAccountCloseManagedMatchesOriginalCacheAndReceipts(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-close-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []closeGoldenCase
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for i := range fixtures {
		f := &fixtures[i]
		t.Run(f.Name, func(t *testing.T) {
			original := f.Expected
			f.Expected = struct {
				Error, Status               string
				Confirmed, Cached           bool
				Revision                    int64
				Saves, Changed              int
				Requests                    []string
				SecondError                 string
				SecondRequests, SecondSaves int
			}{}
			ctx := context.Background()
			secrets := memory.New()
			_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-close-key"))
			requests := []string{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.RequestURI())
				if r.Header.Get("x-api-key") != "synthetic-close-key" {
					t.Error("credential authority changed")
				}
				if r.URL.Path == "/connected_accounts/account_original" && r.Method == "GET" {
					if f.AccountStatus != 0 {
						w.WriteHeader(f.AccountStatus)
						return
					}
					id, user, toolkit := "account_original", "owner", "example"
					if f.WrongID {
						id = "foreign"
					}
					if f.WrongUser {
						user = "foreign"
					}
					if f.WrongToolkit {
						toolkit = "foreign"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": user, "status": "ACTIVE", "is_disabled": f.Disabled, "toolkit": map[string]string{"slug": toolkit}, "data": map[string]string{"access_token": "excluded-private"}})
					return
				}
				if r.URL.Path == "/connected_accounts/account_original/revoke" && r.Method == "POST" {
					if f.RevokeStatus != 0 {
						w.WriteHeader(f.RevokeStatus)
						return
					}
					body := f.RevokeBody
					if body == "" {
						body = `{"connected_account":{"id":"account_original","status":"REVOKED"}}`
					}
					_, _ = w.Write([]byte(body))
					return
				}
				if r.URL.Path == "/connected_accounts/account_original" && r.Method == "DELETE" {
					if f.DeleteStatus != 0 {
						w.WriteHeader(f.DeleteStatus)
					} else {
						w.WriteHeader(204)
					}
					return
				}
				t.Error("unexpected source", r.Method, r.URL)
				http.NotFound(w, r)
			}))
			defer peer.Close()
			c := Connection{ID: "connection_original", Toolkit: "example", Alias: "work", AccountID: "account_original", Status: "active", Public: f.Public}
			if f.InvalidAccount {
				c.AccountID = "bad/id"
			}
			if f.AlreadyClosed {
				c.Status = "disconnected"
				c.RevocationConfirmed = true
			}
			store := &closeGoldenStore{state: State{Revision: 7, KeyRef: "credential:fixture", UserID: "owner", Connections: []Connection{c}}, failAt: f.FailSave}
			if f.Missing {
				store.state.Connections = nil
			}
			changed := 0
			s := &Service{Store: store, Secrets: secrets, Client: composio.Client{Endpoint: peer.URL}, ReadAccountClose: func(context.Context) (AccountClosePolicy, error) {
				return ManagedAccountClose(func(c context.Context, revision int64, request contract.AccountCloseRequest) (contract.AccountClosePlan, error) {
					if revision != 8 {
						t.Fatal("lost post-closure CAS revision", revision)
					}
					raw, _ := json.Marshal(request)
					result, err := appsaccounts.Handlers().Services[contract.AccountClose](c, rsiplugin.Call{Input: raw})
					if err != nil {
						return contract.AccountClosePlan{}, err
					}
					var plan contract.AccountClosePlan
					err = rsiplugin.DecodeResult(result.Output, &plan)
					return plan, err
				}), nil
			}, pending: map[string]authorization{"connection_original": {Created: time.Now()}}, Changed: func(context.Context) error {
				changed++
				if f.ChangedFail {
					return errors.New("private callback")
				}
				return nil
			}}
			err := s.Disconnect(ctx, "connection_original")
			f.Expected.Error = closeGoldenError(err)
			f.Expected.Revision = store.state.Revision
			f.Expected.Saves = store.writes
			f.Expected.Changed = changed
			f.Expected.Requests = append([]string{}, requests...)
			if len(store.state.Connections) != 0 {
				f.Expected.Status = store.state.Connections[0].Status
				f.Expected.Confirmed = store.state.Connections[0].RevocationConfirmed
			}
			_, f.Expected.Cached = s.pending["connection_original"]
			if f.Expected.Status == "disconnected" {
				beforeRequests, beforeSaves := len(requests), store.writes
				f.Expected.SecondError = closeGoldenError(s.Disconnect(ctx, "connection_original"))
				f.Expected.SecondRequests = len(requests) - beforeRequests
				f.Expected.SecondSaves = store.writes - beforeSaves
			}
			if !reflect.DeepEqual(f.Expected, original) {
				t.Fatal("managed cleanup changed original cache/receipt/effect", f.Expected, original)
			}
		})
	}
}

func TestAppsAccountCloseHostRejectsForgedOrderAndKeepsSourceProof(t *testing.T) {
	ctx := context.Background()
	for _, f := range []struct {
		name                       string
		after                      int
		kind                       string
		wrongIdentity, readFailure bool
		wantCalls                  int
		wantProof                  bool
	}{
		{"skip-identity", 0, "revoke", false, false, 0, false},
		{"delete-before-account", 0, "delete", false, false, 0, false},
		{"repeat-account", 1, "account", false, false, 1, false},
		{"foreign-account", 1, "revoke", true, false, 1, false},
		{"failed-account", 1, "revoke", false, true, 1, false},
		{"delete-without-attempt", 1, "delete", false, false, 1, false},
		{"repeat-revoke", 2, "revoke", false, false, 2, true},
		{"repeat-delete", 3, "delete", false, false, 3, true},
		{"invented-confirmation", 0, "confirm", false, false, 0, false},
	} {
		t.Run(f.name, func(t *testing.T) {
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method == "GET" {
					if f.readFailure {
						w.WriteHeader(502)
						return
					}
					id := "account_original"
					if f.wrongIdentity {
						id = "foreign"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": "owner", "toolkit": map[string]string{"slug": "example"}})
					return
				}
				if r.Method == "POST" {
					_, _ = w.Write([]byte(`{"connected_account":{"id":"account_original","status":"REVOKED"}}`))
					return
				}
				w.WriteHeader(204)
			}))
			defer peer.Close()
			secrets := memory.New()
			_ = secrets.Put(ctx, "credential:fixture", []byte("synthetic-key"))
			index := 0
			prefix := []string{"account", "revoke", "delete"}
			p := ManagedAccountClose(func(context.Context, int64, contract.AccountCloseRequest) (contract.AccountClosePlan, error) {
				kind := f.kind
				if index < f.after {
					kind = prefix[index]
				}
				index++
				return contract.AccountClosePlan{Kind: kind}, nil
			})
			proof := p.cleanup(ctx, State{Revision: 8, UserID: "owner"}, composio.Client{Endpoint: peer.URL, Secrets: secrets, KeyRef: "credential:fixture"}, Connection{AccountID: "account_original", Toolkit: "example"})
			if calls != f.wantCalls || proof != f.wantProof {
				t.Fatal("forged cleanup escaped order/identity/proof", calls, proof)
			}
		})
	}
	// A failed selector cannot suppress local owner closure or turn a policy error
	// into a native source fallback, even if the old native client could work.
	store := &closeGoldenStore{state: State{Revision: 7, Connections: []Connection{{ID: "owner", Toolkit: "example", AccountID: "account_original", Status: "active"}}}}
	s := &Service{Store: store, ReadAccountClose: func(context.Context) (AccountClosePolicy, error) {
		return NativeAccountClose(), errors.New("candidate unavailable")
	}}
	if err := s.Disconnect(ctx, "owner"); err != nil || store.state.Connections[0].Status != "disconnected" || store.state.Connections[0].RevocationConfirmed || store.writes != 2 {
		t.Fatal("failed candidate blocked owner closure or invented proof", store.state, err)
	}
}
