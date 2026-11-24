// Server runs the provider side of the bounded, isolated model RPC.
// It has no access to domain stores or tools.
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
)

const Version = "steward-go-v1"

type job struct {
	cancel              context.CancelFunc
	done                chan struct{}
	session, connection string
	suppressed          atomic.Bool
}
type Server struct {
	HTTPClient  *http.Client
	mu          sync.Mutex
	writeMu     sync.Mutex
	authMu      sync.Mutex
	jobs        map[string]*job
	initialized bool
	out         io.Writer
	auth        accountAuth
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	s.out = out
	s.jobs = map[string]*job{}
	s.auth.client = s.client()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait(); s.stopJobs(""); s.auth.cancel() }()
	for scanner.Scan() {
		var f frame
		if json.Unmarshal(scanner.Bytes(), &f) != nil || f.JSONRPC != "2.0" || f.ID < 1 || f.ID > 9007199254740991 {
			return provider.ErrInvalidResponse
		}
		workers.Add(1)
		go func() { defer workers.Done(); s.handle(ctx, f) }()
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}
func (s *Server) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (s *Server) send(v any) bool {
	data, err := json.Marshal(v)
	if err != nil || len(data) >= MaxFrameBytes {
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.out.Write(append(data, '\n'))
	return err == nil
}
func (s *Server) reply(f frame, v any, err error) {
	if err != nil {
		s.send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32000, "message": provider.ErrorCode(err)}})
	} else {
		if !s.send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": v}) {
			s.send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32000, "message": "INVALID_PROVIDER_RESPONSE"}})
		}
	}
}
func (s *Server) stopJobs(session string) {
	s.mu.Lock()
	jobs := []*job{}
	for _, j := range s.jobs {
		if session == "" || j.session == session {
			j.suppressed.Store(true)
			j.cancel()
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()
	for _, j := range jobs {
		<-j.done
	}
}
func (s *Server) handle(ctx context.Context, f frame) {
	if strings.HasPrefix(f.Method, "auth.") && f.Method != "auth.refresh" {
		s.authMu.Lock()
		defer s.authMu.Unlock()
	}
	var p struct {
		ProtocolVersion int             `json:"protocol_version"`
		GenerationID    string          `json:"generation_id"`
		SessionID       string          `json:"session_id"`
		ConnectionID    string          `json:"connection_id"`
		Connection      llm.Connection  `json:"connection"`
		Credential      json.RawMessage `json:"credential"`
		ProviderKind    string          `json:"provider_kind"`
		WireAPI         string          `json:"wire_api"`
		Endpoint        string          `json:"endpoint"`
	}
	if json.Unmarshal(f.Params, &p) != nil {
		s.reply(f, nil, provider.ErrInvalidResponse)
		return
	}
	if f.Method == "bridge.initialize" {
		if p.ProtocolVersion != ProtocolVersion {
			s.reply(f, nil, provider.ErrIncompatible)
			return
		}
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		s.reply(f, map[string]any{"protocol_version": ProtocolVersion}, nil)
		return
	}
	s.mu.Lock()
	ready := s.initialized
	s.mu.Unlock()
	if !ready {
		s.reply(f, nil, provider.ErrIncompatible)
		return
	}
	switch f.Method {
	case "generation.start", "generation.continue":
		var r generationRequest
		if json.Unmarshal(f.Params, &r) != nil || r.GenerationID == "" {
			s.reply(f, nil, provider.ErrInvalidResponse)
			return
		}
		work, cancel := context.WithCancel(ctx)
		j := &job{cancel: cancel, done: make(chan struct{}), session: r.SessionID, connection: r.Connection.ConnectionID}
		s.mu.Lock()
		if s.jobs[r.GenerationID] != nil {
			s.mu.Unlock()
			cancel()
			s.reply(f, nil, provider.ErrInvalidResponse)
			return
		}
		s.jobs[r.GenerationID] = j
		s.mu.Unlock()
		s.reply(f, map[string]any{}, nil)
		go func() {
			defer close(j.done)
			defer cancel()
			defer func() { s.mu.Lock(); delete(s.jobs, r.GenerationID); s.mu.Unlock() }()
			s.generate(work, r, j)
		}()
	case "generation.cancel":
		s.mu.Lock()
		j := s.jobs[p.GenerationID]
		if j != nil {
			j.suppressed.Store(true)
			j.cancel()
		}
		s.mu.Unlock()
		if j != nil {
			<-j.done
		}
		s.reply(f, map[string]any{}, nil)
	case "session.dispose":
		s.stopJobs(p.SessionID)
		s.reply(f, map[string]any{}, nil)
	case "bridge.shutdown":
		s.stopJobs("")
		s.auth.cancel()
		s.reply(f, map[string]any{}, nil)
	case "model.probe":
		if p.ProviderKind == "openai_account" && p.WireAPI == "openai_responses" && p.Endpoint == "" {
			s.reply(f, map[string]any{}, nil)
			return
		}
		err := validateEndpoint(p.Endpoint)
		if p.ProviderKind != "openai_compatible" || p.WireAPI != "openai_completions" {
			err = provider.ErrIncompatible
		}
		s.reply(f, map[string]any{}, err)
	case "models.list", "models.refresh":
		v, err := s.catalog(ctx, p.Connection)
		s.reply(f, v, err)
	case "auth.start":
		v, err := s.auth.start(ctx, f.Params)
		s.reply(f, v, err)
	case "auth.continue":
		v, err := s.auth.poll()
		s.reply(f, v, err)
	case "auth.cancel":
		s.auth.cancel()
		s.reply(f, map[string]any{}, nil)
	case "auth.status":
		s.reply(f, map[string]any{"status": s.auth.status(), "events": []any{}}, nil)
	case "auth.refresh":
		v, err := s.auth.refresh(ctx, p.Credential)
		s.reply(f, v, err)
	case "auth.logout":
		s.mu.Lock()
		jobs := []*job{}
		for _, j := range s.jobs {
			if j.connection == p.ConnectionID {
				j.suppressed.Store(true)
				j.cancel()
				jobs = append(jobs, j)
			}
		}
		s.mu.Unlock()
		for _, j := range jobs {
			<-j.done
		}
		s.auth.cancel()
		revoked := s.auth.revoke(ctx, p.Credential)
		s.reply(f, map[string]bool{"revoked": revoked}, nil)
	default:
		s.reply(f, nil, provider.ErrIncompatible)
	}
}
