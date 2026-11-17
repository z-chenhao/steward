package connectedapps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"strings"
	"testing"
	"time"
)

type checkGoldenCase struct {
	Name, Previous, SourceStatus                                                                          string
	Disabled, Public, NoAuth, WrongID, WrongUser, WrongToolkit, SaveFail, ChangedFail, Missing, Expansion bool
	HTTPStatus                                                                                            int
	Expected                                                                                              struct {
		Error, Status            string
		Revision                 int64
		Saves, Changed, Requests int
		Cached                   bool
		NormalizedStatusBytes    int
		NormalizedStatusSHA256   string
	}
}
type checkGoldenStore struct {
	state State
	saves int
	fail  bool
}

func (s *checkGoldenStore) LoadApps(context.Context) (State, error) {
	v := s.state
	v.Connections = append([]Connection(nil), v.Connections...)
	return v, nil
}
func (s *checkGoldenStore) SaveApps(_ context.Context, v State, revision int64) error {
	s.saves++
	if s.fail {
		return ErrConflict
	}
	if revision != s.state.Revision {
		return ErrConflict
	}
	s.state = v
	s.state.Revision++
	return nil
}
func checkGoldenError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrDenied):
		return "denied"
	case errors.Is(err, ErrConflict):
		return "conflict"
	}
	return "callback"
}
func TestAppsAccountCheckManagedMatchesOriginalCacheAndReceipts(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-check-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []checkGoldenCase
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := range fixtures {
		f := &fixtures[i]
		t.Run(f.Name, func(t *testing.T) {
			original := f.Expected
			f.Expected = struct {
				Error, Status            string
				Revision                 int64
				Saves, Changed, Requests int
				Cached                   bool
				NormalizedStatusBytes    int
				NormalizedStatusSHA256   string
			}{}
			secrets := memory.New()
			key := "synthetic-check-key"
			if f.Expansion {
				key = "q"
			}
			_ = secrets.Put(ctx, "credential:fixture", []byte(key))
			requests := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if f.HTTPStatus != 0 {
					w.WriteHeader(f.HTTPStatus)
					return
				}
				toolkit := "example"
				if f.WrongToolkit {
					toolkit = "foreign"
				}
				if f.Public {
					_ = json.NewEncoder(w).Encode(composio.Toolkit{Slug: toolkit, NoAuth: f.NoAuth})
					return
				}
				id, user := "account_original", "owner"
				if f.WrongID {
					id = "foreign"
				}
				if f.WrongUser {
					user = "foreign"
				}
				status := f.SourceStatus
				if f.Expansion {
					status = strings.Repeat("q", 1<<20)
				}
				if r.URL.Path != "/connected_accounts/account_original" {
					t.Error("wrong source", r.URL)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": user, "status": status, "is_disabled": f.Disabled, "toolkit": map[string]string{"slug": toolkit}, "data": map[string]string{"access_token": "excluded-private"}})
			}))
			defer peer.Close()
			c := Connection{ID: "connection_original", Toolkit: "example", AccountID: "account_original", Alias: "work", Status: f.Previous, Public: f.Public}
			store := &checkGoldenStore{state: State{Revision: 7, KeyRef: "credential:fixture", UserID: "owner", Connections: []Connection{c}}, fail: f.SaveFail}
			if f.Missing {
				store.state.Connections = nil
			}
			changed := 0
			s := &Service{Store: store, Secrets: secrets, Client: composio.Client{Endpoint: peer.URL}, ReadAccountPolicies: func(context.Context) (AccountPolicies, error) {
				return NativeAccountPolicies().WithAccountCheck(ManagedAccountCheck(func(c context.Context, revision int64, request contract.AccountCheckRequest) (contract.AccountCheckPlan, error) {
					if revision != 7 {
						t.Fatal("lost original revision", revision)
					}
					input, _ := json.Marshal(request)
					result, err := appsaccounts.Handlers().Services[contract.AccountCheck](c, rsiplugin.Call{Input: input})
					if err != nil {
						return contract.AccountCheckPlan{}, err
					}
					var plan contract.AccountCheckPlan
					err = rsiplugin.DecodeResult(result.Output, &plan)
					return plan, err
				})), nil
			}, pending: map[string]authorization{"connection_original": {Created: time.Now(), Link: composio.Link{ConnectionID: "connection_original"}}}, Changed: func(context.Context) error {
				changed++
				if f.ChangedFail {
					return errors.New("private callback")
				}
				return nil
			}}
			err := s.Check(ctx, "connection_original")
			f.Expected.Error = checkGoldenError(err)
			f.Expected.Revision = store.state.Revision
			f.Expected.Saves = store.saves
			f.Expected.Changed = changed
			f.Expected.Requests = requests
			if len(store.state.Connections) != 0 {
				f.Expected.Status = store.state.Connections[0].Status
			}
			_, f.Expected.Cached = s.pending["connection_original"]
			if f.Expansion {
				a, err := s.client(store.state).Account(ctx, "account_original")
				if err != nil {
					t.Fatal(err)
				}
				h := sha256.Sum256([]byte(a.Status))
				f.Expected.NormalizedStatusBytes = len(a.Status)
				f.Expected.NormalizedStatusSHA256 = hex.EncodeToString(h[:])
			}
			if !reflect.DeepEqual(f.Expected, original) {
				t.Fatal("managed policy changed original cache/receipt outcome", f.Expected, original)
			}
		})
	}
}

func TestAppsAccountCheckHostRejectsInventedStatusAuthority(t *testing.T) {
	for _, fixture := range []struct {
		request contract.AccountCheckRequest
		status  string
	}{
		{contract.AccountCheckRequest{}, "active"},
		{contract.AccountCheckRequest{Active: true, Disabled: true}, "active"},
		{contract.AccountCheckRequest{}, "pending"},
		{contract.AccountCheckRequest{Initiated: true, Disabled: true}, "pending"},
		{contract.AccountCheckRequest{Active: true}, "pending"},
		{contract.AccountCheckRequest{Public: true}, "active"},
		{contract.AccountCheckRequest{Public: true}, "expired"},
		{contract.AccountCheckRequest{Active: true}, "refresh"},
		{contract.AccountCheckRequest{Active: true}, "invented"},
	} {
		p := ManagedAccountCheck(func(context.Context, int64, contract.AccountCheckRequest) (contract.AccountCheckPlan, error) {
			return contract.AccountCheckPlan{Status: fixture.status}, nil
		})
		if status, err := p.decide(context.Background(), 7, fixture.request); status != "" || !errors.Is(err, ErrUnavailable) {
			t.Fatal("invented source authority accepted", fixture, status, err)
		}
	}
	if _, err := (AccountCheckPolicy{}).decide(context.Background(), 7, contract.AccountCheckRequest{Active: true}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("zero managed policy inferred native", err)
	}
}
