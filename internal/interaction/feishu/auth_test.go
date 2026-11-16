package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/delivery"
)

func managedClient(base string) *Client {
	c := fixtureClient()
	c.Config.APIBase = base
	c.Config.AccessTokenRef = ""
	c.Config.AppSecretRef = "env:APP_SECRET"
	c.Secrets.(secrets)["env:APP_SECRET"] = []byte("private-app-secret")
	return NewClient(c.Config, c.Secrets)
}

func TestManagedTokensSingleFlightEarlyRefreshAndStaleInvalidation(t *testing.T) {
	var auth, sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			var q map[string]string
			if json.NewDecoder(r.Body).Decode(&q) != nil || q["app_id"] != "app" || q["app_secret"] != "private-app-secret" || r.Header.Get("Authorization") != "" {
				t.Error("wrong token request")
			}
			token := auth.Add(1)
			fmt.Fprintf(w, `{"code":0,"tenant_access_token":"token-%d","expire":600}`, token)
			return
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer token-%d", auth.Load()) {
			t.Error("stale access token")
		}
		sends.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"message_id":"receipt"}}`)
	}))
	defer server.Close()
	c := managedClient(server.URL)
	defer c.Close()
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	c.tokens.now = func() time.Time { return time.Unix(0, clock.Load()) }
	sendBatch := func() {
		t.Helper()
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Go(func() {
				copy := *c
				if _, err := copy.Send(t.Context(), "bound-chat", "key", "text", map[string]string{"text": "hello"}); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
	}
	sendBatch()
	if auth.Load() != 1 || sends.Load() != 32 {
		t.Fatal("refresh was not shared", auth.Load(), sends.Load())
	}
	clock.Add(int64(550 * time.Second))
	sendBatch()
	if auth.Load() != 2 || sends.Load() != 64 {
		t.Fatal("early refresh failed", auth.Load(), sends.Load())
	}
	old, _ := http.NewRequest("GET", server.URL, nil)
	old.Header.Set("Authorization", "Bearer token-1")
	c.invalidate(&http.Response{Request: old})
	if _, err := c.accessToken(t.Context()); err != nil || auth.Load() != 2 {
		t.Fatal("old rejection invalidated the fresh token", err, auth.Load())
	}
	c.Close()
	if len(c.tokens.token) != 0 {
		t.Fatal("cache retained credentials on close")
	}
}

func TestRefreshWaiterCancellationDoesNotCancelOwner(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"code":0,"tenant_access_token":"shared-token","expire":600}`)
	}))
	defer server.Close()
	c := managedClient(server.URL)
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.accessToken(t.Context()); done <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.accessToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("waiter could not cancel", err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal("owner was cancelled", err)
	}
}

func TestCredentialRejectionIsNotAnAmbiguousSendAndNeverResendsInClient(t *testing.T) {
	var auth, sends atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			auth.Add(1)
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"token","expire":600}`)
			return
		}
		if sends.Add(1) == 1 {
			fmt.Fprint(w, `{"code":99991663,"msg":"secret-bearing body must not leak"}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"message_id":"receipt"}}`)
	}))
	defer server.Close()
	c := managedClient(server.URL)
	defer c.Close()
	_, err := c.Send(t.Context(), "bound-chat", "key", "text", "hello")
	if !errors.Is(err, ErrAuthentication) || sends.Load() != 1 || auth.Load() != 1 {
		t.Fatal("client retried or lost explicit rejection", err, sends.Load(), auth.Load())
	}
	if observation := credentialObservation(err); observation.Outcome != delivery.Retry {
		t.Fatal("rejected credential became uncertain", observation)
	}
	if _, err = c.Send(t.Context(), "bound-chat", "key", "text", "hello"); err != nil || auth.Load() != 2 || sends.Load() != 2 {
		t.Fatal("next authorized attempt did not refresh", err)
	}
	if credentialObservation(ErrProtocol).Outcome != delivery.Unknown {
		t.Fatal("ambiguous response authorized resend")
	}
	if credentialObservation(ErrCredentialsRejected).Outcome != delivery.Failed {
		t.Fatal("invalid app credentials retried")
	}
}

func TestTokenFailuresAreSanitizedAndCannotReachMessageEndpoint(t *testing.T) {
	for _, reply := range []string{
		`{"code":0,"expire":600}`, `{"tenant_access_token":"sensitive","expire":600}`,
		`{"code":0,"tenant_access_token":"sensitive","expire":0}`, `{"code":0,"tenant_access_token":"sensitive","expire":9999999999999}`,
		`{"code":0,"tenant_access_token":"bad\ntoken","expire":600}`, `{"code":10003,"msg":"private-app-secret sensitive"}`,
	} {
		t.Run(reply, func(t *testing.T) {
			var sends atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "tenant_access_token") {
					sends.Add(1)
				}
				fmt.Fprint(w, reply)
			}))
			defer server.Close()
			c := managedClient(server.URL)
			defer c.Close()
			_, err := c.Send(t.Context(), "bound-chat", "key", "text", "hello")
			if err == nil || sends.Load() != 0 || strings.Contains(err.Error(), "private-app-secret") || strings.Contains(err.Error(), "sensitive") {
				t.Fatal("invalid token leaked or reached transport", err)
			}
			if !errors.Is(err, ErrCredentialsUnavailable) && !errors.Is(err, ErrCredentialsRejected) {
				t.Fatal("pre-send failure lost classification", err)
			}
		})
	}
}

func TestTokenRefreshRedirectCannotLeakAppSecretEvenWithCustomHTTPClient(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c := managedClient(redirect.URL)
	c.HTTP = &http.Client{}
	if _, err := c.accessToken(t.Context()); err == nil || leaked.Load() {
		t.Fatal("redirect leaked application secret", err)
	}
}

func TestConnectionCheckRefreshesOnlyReadOnlyProbeAndReportsSanitizedFailures(t *testing.T) {
	var auth, probes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			auth.Add(1)
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"probe-token","expire":600}`)
		case "/open-apis/bot/v3/info/":
			if r.Method != "GET" {
				t.Error("probe has side effects")
			}
			if probes.Add(1) == 1 {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"code":99991663}`)
			} else {
				fmt.Fprint(w, `{"code":0,"bot":{"open_id":"bot-id"}}`)
			}
		default:
			t.Error("unexpected side effect", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c := managedClient(server.URL)
	defer c.Close()
	if status := c.ConnectionFacts(); status.State != "unchecked" || auth.Load() != 0 {
		t.Fatal("status performed network access", status)
	}
	status := c.CheckFacts(t.Context())
	if status.State != "ready" || status.ExpiresAt == nil || auth.Load() != 2 || probes.Load() != 2 {
		t.Fatal(status, auth.Load(), probes.Load())
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "probe-token") || strings.Contains(string(raw), "private-app-secret") {
		t.Fatal("diagnostic leaked credentials")
	}
	c.Secrets = secrets{}
	c.invalidate(&http.Response{Request: func() *http.Request {
		r, _ := http.NewRequest("GET", server.URL, nil)
		r.Header.Set("Authorization", "Bearer probe-token")
		return r
	}()})
	if status := c.CheckFacts(t.Context()); status.State != "credentials_unavailable" {
		t.Fatal("missing credential was not classified", status)
	}
}

func TestExpiredCredentialFailureNeverFallsBackToOldToken(t *testing.T) {
	var requests, sends atomic.Int64
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			requests.Add(1)
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"expires","expire":10}`)
			return
		}
		sends.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"message_id":"receipt"}}`)
	}))
	defer server.Close()
	c := managedClient(server.URL)
	defer c.Close()
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c.tokens.now = func() time.Time { return time.Unix(0, now.Load()) }
	if _, err := c.accessToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	now.Add(int64(11 * time.Second))
	fail.Store(true)
	for range 3 {
		if _, err := c.Send(t.Context(), "bound-chat", "key", "text", "hello"); !errors.Is(err, ErrCredentialsUnavailable) {
			t.Fatal("expired fallback", err)
		}
	}
	if requests.Load() != 2 || sends.Load() != 0 {
		t.Fatal("failure stampede or expired token sent", requests.Load(), sends.Load())
	}
	now.Add(int64(2 * time.Second))
	fail.Store(false)
	if _, err := c.Send(t.Context(), "bound-chat", "key", "text", "hello"); err != nil || sends.Load() != 1 {
		t.Fatal("temporary outage did not recover", err)
	}
}

func TestClosingClientCannotPublishInFlightCredentials(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"code":0,"tenant_access_token":"late-secret","expire":600}`)
	}))
	defer server.Close()
	defer once.Do(func() { close(release) })
	c := managedClient(server.URL)
	done := make(chan error, 1)
	go func() { _, err := c.accessToken(t.Context()); done <- err }()
	<-started
	c.Close()
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, ErrCredentialsUnavailable) || len(c.tokens.token) != 0 {
		t.Fatal("closed client retained late token", err)
	}
}

func TestServerFailureCannotAuthorizeCredentialRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"code":99991663,"data":{"message_id":"untrusted"}}`)
	}))
	defer server.Close()
	c := fixtureClient()
	c.Config.APIBase = server.URL
	_, err := c.Send(t.Context(), "bound-chat", "key", "text", "hello")
	if !errors.Is(err, ErrProtocol) || credentialObservation(err).Outcome != delivery.Unknown {
		t.Fatal("server failure authorized a resend", err)
	}
}
