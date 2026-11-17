// Package web presents the loopback control and conversation surface.
// HTTP handlers do not implement a second execution or approval lifecycle.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/account"
	modelmenu "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	workbench "github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/internal/task"
)

type Config struct {
	Listen   string `yaml:"listen"`
	TokenRef string `yaml:"token_ref"`
}

func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || (host != "127.0.0.1" && host != "::1") || c.TokenRef != "" && !interaction.ValidCredentialRef(c.TokenRef) {
		//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
		return errors.New("Web requires a literal loopback listener and an optional valid credential reference")
	}
	return nil
}

const Subject = interaction.WebSubject
const Destination = interaction.WebDestination

type Status struct {
	Readiness            string  `json:"readiness"`
	Status               string  `json:"status"`
	ConfigVersion        int64   `json:"config_version"`
	ActiveModelProfileID *string `json:"active_model_profile_id,omitempty"`
}
type Core interface {
	WaitForRuntime(context.Context) error
	ChannelBindings(context.Context) ([]interaction.Binding, error)
	ChannelPreference(context.Context) (interaction.Preference, error)
	PreferChannel(context.Context, string, string, int64) error
	WebStatus(context.Context) (Status, error)
	Conversation(context.Context, int64) (conversation.Page, error)
	Activity(context.Context) ([]task.Summary, error)
	WebApprovals(context.Context) ([]interaction.Approval, error)
	AcceptInbound(context.Context, interaction.InboundEvent) (agent.Receipt, error)
	DecideInbound(context.Context, interaction.InboundEvent, string, string, string) error
	CheckChannel(context.Context, string, string, string) error
	ImportChannelArtifact(context.Context, interaction.InboundEvent, string, artifact.Input, io.Reader) (string, error)
	SaveArtifact(context.Context, artifact.Input, io.Reader) (artifact.Descriptor, error)
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
	DescribeArtifact(context.Context, string) (artifact.Descriptor, error)
	WebProjection(context.Context, conversation.Message) (json.RawMessage, error)
}
type Models interface {
	ListProfiles(context.Context) ([]llm.Profile, error)
	AddProfile(context.Context, llm.ProfileInput) (llm.Profile, error)
}
type Server struct {
	WebWorkbench          func(context.Context, string) (workbench.Presentation, string, bool, error)
	modelMu               sync.Mutex
	appLogos              appLogoCache
	Interface             AppInterfaceReader
	Logos                 AppLogoReader
	Apps                  AppConnections
	Computer              ComputerReader
	Capabilities          CapabilitySettings
	CapabilityForms       CapabilityFormProjector
	Workers               WorkerReader
	Inspector             ExecutionReader
	Memory                MemoryReader
	Contexts              ContextReader
	Traces                TraceReader
	Tasks                 TaskReader
	Feedbacks             FeedbackSettings
	Schedules             ScheduleSettings
	Config                Config
	Core                  Core
	Models                Models
	Control               llm.Control
	Accounts              *account.Manager
	Secrets               llm.SecretStore
	ModelConnectionPolicy func(context.Context, modelmenu.ConnectionFacts) (modelmenu.ConnectionDefaults, bool, error)
	ModelSettings         func(context.Context, modelmenu.SettingsFacts) (modelmenu.SettingsProjection, bool, error)
	ModelPicker           func(context.Context, string) (modelmenu.PickerPresentation, error)
	ModelMenu             func(context.Context, modelmenu.Facts) (modelmenu.Projection, bool, error)
	AccountCatalog        func(context.Context, string) ([]llm.CatalogModel, error)
	Callback              http.Handler
	Wake                  func()
	uiPreviews            uiPreview
	replies               replyPreview
	mu                    sync.Mutex
	requests              sync.WaitGroup
	stopping              bool
	session               string
	sessionExpires        time.Time
	auth                  llm.AuthState
	authOnResources       bool
	authError             string
	accountID             string
	authCancel            context.CancelFunc
	authDone              chan struct{}
	lifetime              context.Context
}

func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	if s.Config.TokenRef == "" {
		address, ok := l.Addr().(*net.TCPAddr)
		if !ok || !address.IP.IsLoopback() {
			return errors.New("token-free Web requires a loopback listener")
		}
	}
	s.lifetime = ctx
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	var err error
	select {
	case err = <-served:
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		_ = srv.Close()
	case <-ctx.Done():
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if shutdownErr := srv.Shutdown(stop); shutdownErr != nil {
			_ = srv.Close()
		}
		cancel()
		err = <-served
	}
	// Admission is closed under the same lock used before requests.Add. No HTTP
	// handler or login survives the server's foreground lifetime or App.Close.
	s.requests.Wait()
	s.mu.Lock()
	cancel, authDone := s.authCancel, s.authDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if authDone != nil {
		<-authDone
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func jsonResult(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int) { http.Error(w, http.StatusText(status), status) }
func (s *Server) authorized(r *http.Request) bool {
	if s.Config.TokenRef == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		return err == nil && net.ParseIP(host).IsLoopback()
	}
	if s.Secrets == nil {
		return false
	}
	token, e := s.Secrets.Get(r.Context(), s.Config.TokenRef)
	if e != nil || len(token) < 24 {
		return false
	}
	defer func() {
		for i := range token {
			token[i] = 0
		}
	}()
	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && subtle.ConstantTimeCompare([]byte(value), token) == 1 {
		return true
	}
	cookie, e := r.Cookie("steward_session")
	if e != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.sessionExpires) && s.session != "" && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.session)) == 1
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		fail(w, 503)
		return
	}
	s.requests.Add(1)
	s.mu.Unlock()
	defer s.requests.Done()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.URL.Path == "/feishu/callback" && s.Callback != nil {
		s.Callback.ServeHTTP(w, r)
		return
	}
	// Reject DNS rebinding, cross-site POSTs and wildcard Origin. No CORS API.
	host, _, e := net.SplitHostPort(r.Host)
	if e != nil || (host != "127.0.0.1" && host != "::1") {
		fail(w, 403)
		return
	}
	// Without a configured token, only the direct loopback peer is trusted.
	// Fetch Metadata and Origin reject other websites, including another local
	// port; forwarded headers never supply identity. Bindings still gate routes.
	if s.Config.TokenRef == "" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			fail(w, 403)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" && site != "same-origin" {
			fail(w, 403)
			return
		}
	}
	if r.Method != "GET" && r.Method != "POST" {
		fail(w, 405)
		return
	}
	if r.Method == "POST" {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			fail(w, 403)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	}
	if serveAsset(w, r) {
		return
	}
	if r.URL.Path == "/session" && r.Method == "POST" && s.Config.TokenRef != "" {
		if s.Secrets == nil {
			fail(w, 503)
			return
		}
		if r.ParseForm() != nil {
			fail(w, 400)
			return
		}
		token, e := s.Secrets.Get(r.Context(), s.Config.TokenRef)
		if e != nil || len(token) < 24 || subtle.ConstantTimeCompare(token, []byte(r.FormValue("token"))) != 1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(401)
			s.render(w, r, page{Login: true, Error: "That workspace token was not accepted.", Next: safePageReturn(r.FormValue("next"))})
			return
		}
		for i := range token {
			token[i] = 0
		}
		bytes := make([]byte, 32)
		if _, e = rand.Read(bytes); e != nil {
			fail(w, 500)
			return
		}
		session := base64.RawURLEncoding.EncodeToString(bytes)
		s.mu.Lock()
		s.session = session
		s.sessionExpires = time.Now().Add(time.Hour)
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "steward_session", Value: session, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 3600})
		http.Redirect(w, r, safePageReturn(r.FormValue("next")), http.StatusSeeOther)
		return
	}
	if !s.authorized(r) {
		if s.Config.TokenRef == "" {
			fail(w, 403)
			return
		}
		_, view, _ := pageRoute(r.URL.Path)
		if view != "" && r.Method == "GET" {
			if r.URL.Path != "/" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(401)
			}
			s.render(w, r, page{Login: true, Next: safePageReturn(r.URL.RequestURI())})
			return
		}
		fail(w, 401)
		return
	}
	if s.Core.CheckChannel(r.Context(), "web", Subject, Destination) != nil {
		fail(w, 403)
		return
	}
	if r.URL.Path == "/session" && r.Method == "POST" && s.Config.TokenRef == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method == "GET" {
		s.get(w, r)
		return
	}
	if e := s.post(w, r); e != nil {
		s.postFailure(w, r, e)
		return
	}
}

// AuthURL restricts clickable authentication links to the existing provider's
// secure browser flow. Events remain ephemeral, authenticated, and value-free in logs.
func AuthURL(value string) string {
	u, e := url.Parse(value)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return ""
	}
	return value
}
