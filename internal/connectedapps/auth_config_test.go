package connectedapps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	secretmem "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

func TestAppsAuthConfigHostRejectsChangedIntentAndForgedCommands(t *testing.T) {
	ctx := context.Background()
	secrets := secretmem.New()
	if err := secrets.Put(ctx, "credential:auth-unit", []byte("synthetic-key")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"rewrite-explicit", "excess-limit", "foreign-row", "outside-row", "replay-fetch", "forged-fallback", "continue-after-end"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "foreign-row" {
					_, _ = w.Write([]byte(`{"items":[{"id":"ac_wrong","toolkit":{"slug":"other"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`))
				} else {
					_, _ = w.Write([]byte(`{"items":[{"id":"ac_ok","toolkit":{"slug":"example"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`))
				}
			}))
			defer peer.Close()
			p := ManagedAuthConfig(func(_ context.Context, revision int64, request contract.AuthRequest, rows []contract.AuthRow) (contract.AuthPlan, error) {
				if revision != 17 {
					t.Fatal("original revision changed")
				}
				if request.Step == "begin" {
					switch mode {
					case "rewrite-explicit":
						return contract.AuthPlan{Kind: "fetch", Source: "saved"}, nil
					case "excess-limit":
						return contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: 201}, nil
					case "forged-fallback":
						return contract.AuthPlan{Kind: "unreadable"}, nil
					}
					return contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: 200}, nil
				}
				zero := 0
				switch mode {
				case "foreign-row":
					return contract.AuthPlan{Kind: "done", Row: &zero}, nil
				case "outside-row":
					value := len(rows)
					return contract.AuthPlan{Kind: "done", Row: &value}, nil
				case "replay-fetch":
					return contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: 200}, nil
				case "continue-after-end":
					return contract.AuthPlan{Kind: "next", Limit: 200}, nil
				}
				return contract.AuthPlan{Kind: "reject"}, nil
			})
			explicit := ""
			if mode == "rewrite-explicit" {
				explicit = "ac_explicit"
			}
			value, err := p.find(ctx, State{Revision: 17, AuthConfigs: map[string]string{"example": "ac_saved"}}, composio.Client{Endpoint: peer.URL, Secrets: secrets, KeyRef: "credential:auth-unit"}, "example", explicit, false)
			wantCalls := 1
			if mode == "rewrite-explicit" || mode == "excess-limit" || mode == "forged-fallback" {
				wantCalls = 0
			}
			if !errors.Is(err, ErrUnavailable) || value.ID != "" || calls != wantCalls {
				t.Fatal("forged command/reference expanded source or selected invented config", mode, value, calls, err)
			}
		})
	}
	for _, p := range []AuthConfigPolicy{{}, ManagedAuthConfig(nil)} {
		if _, err := p.find(ctx, State{}, composio.Client{}, "example", "", false); !errors.Is(err, ErrUnavailable) {
			t.Fatal("missing policy inferred native", err)
		}
	}
}
func TestAppsAuthConfigHostPinsSourceCursorAndOriginalPrior(t *testing.T) {
	ctx := context.Background()
	secrets := secretmem.New()
	_ = secrets.Put(ctx, "credential:auth-unit", []byte("synthetic-key"))
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("limit") != "100" {
			t.Error("accepted candidate limit changed")
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`{"items":[{"id":"ac_prior","created_at":"2026","toolkit":{"slug":"example"},"status":"ENABLED","is_enabled_for_tool_router":true}],"next_cursor":"opaque +/=&"}`))
			return
		}
		if r.URL.Query().Get("cursor") != "opaque +/=&" {
			t.Error("opaque original cursor changed")
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"ac_new","created_at":"2027","toolkit":{"slug":"example"},"status":"ENABLED","is_enabled_for_tool_router":true}]}`))
	}))
	defer peer.Close()
	count := 0
	p := ManagedAuthConfig(func(_ context.Context, _ int64, r contract.AuthRequest, rows []contract.AuthRow) (contract.AuthPlan, error) {
		count++
		if r.Step == "begin" {
			return contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: 100}, nil
		}
		zero := 0
		if r.Page == 1 {
			if len(rows) != 1 || rows[0].ID != "ac_prior" {
				t.Fatal("original metadata changed")
			}
			return contract.AuthPlan{Kind: "next", Limit: 100, Row: &zero}, nil
		}
		if len(rows) != 2 || rows[0].ID != "ac_prior" || rows[1].ID != "ac_new" {
			raw, _ := json.Marshal(rows)
			t.Fatal("prior reference dropped or recomputed", string(raw))
		}
		return contract.AuthPlan{Kind: "done", Row: &zero}, nil
	})
	value, err := p.find(ctx, State{}, composio.Client{Endpoint: peer.URL, Secrets: secrets, KeyRef: "credential:auth-unit"}, "example", "", false)
	if err != nil || value.ID != "ac_prior" || !value.Readable || calls != 2 || count != 3 {
		t.Fatal("Host replaced candidate order or replayed source", value, calls, count, err)
	}
}
