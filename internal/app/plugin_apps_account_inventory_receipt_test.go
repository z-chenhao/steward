package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	directory "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type inventoryReceiptGateway struct {
	web.AppConnections
	after func(context.Context) error
}

func (g *inventoryReceiptGateway) Connect(c context.Context, slug, alias, auth string) (composio.Link, error) {
	link, err := g.AppConnections.Connect(c, slug, alias, auth)
	if err == nil {
		err = g.after(c)
	}
	return link, err
}

func TestAppsAccountInventoryAcceptedEffectKeepsOriginalLinkWithoutReplay(t *testing.T) {
	for _, accept := range []string{"application/json", "text/html"} {
		for _, unknown := range []bool{false, true} {
			t.Run(accept+fmt.Sprint(unknown), func(t *testing.T) { testAccountInventoryAcceptedReceipt(t, accept, unknown) })
		}
	}
}
func testAccountInventoryAcceptedReceipt(t *testing.T, accept string, unknown bool) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-inventory-receipt-owner-token"
	if err = secrets.Put(ctx, "credential:inventory-owner", []byte(token)); err != nil {
		t.Fatal(err)
	}
	if err = secrets.Put(ctx, "credential:inventory-api", []byte("synthetic-inventory-api-key")); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	if _, err = a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	graph, policy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
	activateInterface(t, ctx, host, "", graph, policy, "inventory-receipt")
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "credential:inventory-api"
	state.UserID = "original-owner"
	if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	var sessions, links atomic.Int32
	const originalURL = "https://app.composio.dev/link/inventory-original-authorization"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/toolkits/example":
			_, _ = w.Write([]byte(`{"slug":"example","name":"Example"}`))
		case "/auth_configs":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/tool_router/session":
			sessions.Add(1)
			_, _ = w.Write([]byte(`{"session_id":"original-session"}`))
		case "/tool_router/session/original-session/link":
			links.Add(1)
			if unknown {
				http.Error(w, "synthetic uncertain provider response", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"redirect_url":"` + originalURL + `","connected_account_id":"original-provider-account"}`))
		default:
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	a.connectedApps.ReadAccountPolicies = func(c context.Context) (connectedapps.AccountPolicies, error) {
		return a.appsAccountPoliciesWithHost(c, host)
	}
	a.connectedApps.ReadInventory = func(c context.Context) (connectedapps.AccountInventoryPolicy, error) {
		return a.appsAccountInventoryWithHost(c, host)
	}
	a.connectedApps.ReadEditorial = func(context.Context, directory.Query) (directory.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
		return directory.Catalogue{}, true, nil, connectedapps.ErrUnavailable
	}
	// This callback runs after the actual pending state/link has been committed.
	// Presentation is then genuinely revoked before the response's scoped RPC.
	gateway := &inventoryReceiptGateway{AppConnections: a.connectedApps, after: func(c context.Context) error { return host.Store.Quarantine(c, graph) }}
	server := &web.Server{Config: web.Config{TokenRef: "credential:inventory-owner"}, Core: a, Secrets: secrets, Apps: gateway}
	post := func(form url.Values, accept string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest("POST", "http://127.0.0.1:8080/apps/configure", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Origin", "http://127.0.0.1:8080")
		request.Header.Set("Accept", accept)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	response := post(url.Values{"action": {"connect"}, "toolkit": {"example"}, "alias": {"work"}}, accept)
	if unknown {
		want := 422
		if accept == "text/html" {
			want = 409
		}
		if response.Code != want || strings.Contains(response.Body.String(), originalURL) || strings.Contains(response.Body.String(), `presentation_unavailable`) || strings.Contains(response.Body.String(), "synthetic uncertain provider response") {
			t.Fatal("unknown effect invented a success receipt", response.Code, response.Body.String())
		}
		actual, e := a.store.LoadApps(ctx)
		if e != nil || len(actual.Connections) != 0 || sessions.Load() != 1 || links.Load() != 1 {
			t.Fatal("unknown effect replayed or fabricated local success", actual, e, sessions.Load(), links.Load())
		}
		return
	}
	var receipt map[string]string
	if accept == "application/json" {
		if err = json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
			t.Fatal(response.Code, response.Body.String(), err)
		}
		if response.Code != 200 || receipt["presentation_unavailable"] != "true" || receipt["authorization_url"] != originalURL || receipt["connection_id"] == "" || receipt["authorization_expires"] == "0" {
			t.Fatal("accepted link lost after real presentation revocation", response.Code, receipt)
		}
	} else if response.Code != 200 || !strings.Contains(response.Body.String(), originalURL) {
		t.Fatal("native effect receipt lost original link", response.Code, response.Body.String())
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil || len(state.Connections) != 1 || state.Connections[0].ID == "" || state.Connections[0].AccountID != "original-provider-account" {
		t.Fatal("original accepted state changed", state, err)
	}
	if accept == "application/json" && state.Connections[0].ID != receipt["connection_id"] {
		t.Fatal("receipt replaced original connection identity")
	}
	response = post(url.Values{"action": {"continue"}, "connection": {state.Connections[0].ID}}, "text/html")
	if response.Code != 200 || !strings.Contains(response.Body.String(), originalURL) {
		t.Fatal("native accepted receipt lost original link", response.Code, response.Body.String())
	}
	if sessions.Load() != 1 || links.Load() != 1 {
		t.Fatal("presentation failure replayed provider effect", sessions.Load(), links.Load())
	}
	if strings.Contains(response.Body.String(), "synthetic-inventory-api-key") || strings.Contains(response.Body.String(), "original-provider-account") {
		t.Fatal("private provider identity entered recovery receipt")
	}
}
