// Package bridge owns the versioned, private stdio Provider RPC. It never
// exposes child stderr or provider response bodies as errors.
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
)

const ProtocolVersion = 1
const MaxFrameBytes = 1 << 20
const MaxOutputBytes = 64 << 10

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type process struct {
	cmd      *exec.Cmd
	in       *os.File
	write    chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	pending  map[uint64]chan frame
	events   map[string]*eventStream
	done     chan struct{}
	wait     chan struct{}
	failure  error
}

type Client struct {
	mu               sync.Mutex
	path             string
	args             []string
	p                *process
	closed           bool
	initializing     *initialization
	closeDone        chan struct{}
	disposedSessions map[string]bool
	next             atomic.Uint64
	resolver         llm.ConnectionResolver
	observer         llm.GenerationObserver
	dispatchObserver func(llm.GenerationRequest, llm.Dispatch)
}

type initialization struct {
	done chan struct{}
	err  error // published by closing done
}

type dispatchGuardKey struct{}

// Open takes a Host-selected executable and arguments, never model input.
// The child receives no inherited credentials, application configuration or DB
// paths. This is a process boundary, not an OS filesystem/network sandbox.
func Open(ctx context.Context, path string, args ...string) (*Client, error) {
	c := &Client{path: path, args: append([]string(nil), args...)}
	if _, err := c.ensure(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) ensure(ctx context.Context) (*process, error) {
	for {
		if ctx.Err() != nil {
			return nil, llm.ProviderUnavailable
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, llm.ProviderUnavailable
		}
		if pending := c.initializing; pending != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, llm.ProviderUnavailable
			case <-pending.done:
			}
			if ctx.Err() != nil {
				return nil, llm.ProviderUnavailable
			}
			if pending.err != nil {
				return nil, pending.err
			}
			continue
		}
		if c.p != nil {
			select {
			case <-c.p.done:
			default:
				p := c.p
				c.mu.Unlock()
				return p, nil
			}
		}
		previous := c.p
		pending := &initialization{done: make(chan struct{})}
		c.p = nil
		c.initializing = pending
		c.mu.Unlock()
		// No process I/O or reap runs under the lifecycle state lock. Waiters can
		// abandon this initialization independently, and Close can stop its child.
		if previous != nil {
			c.stop(previous)
		}
		p, err := c.startProcess(ctx)
		c.mu.Lock()
		if c.closed {
			err = llm.ProviderUnavailable
		}
		if err != nil {
			c.p = nil
		}
		pending.err = err
		c.initializing = nil
		close(pending.done)
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}

func (c *Client) startProcess(ctx context.Context) (*process, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed || ctx.Err() != nil {
		return nil, llm.ProviderUnavailable
	}
	cmd := exec.Command(c.path, c.args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Dir = os.TempDir()
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, llm.ProviderUnavailable
	}
	in, ok := input.(*os.File)
	if !ok {
		_ = input.Close()
		return nil, llm.ProviderUnavailable
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, llm.ProviderUnavailable
	}
	if cmd.Start() != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, llm.ProviderUnavailable
	}
	p := &process{cmd: cmd, in: in, write: make(chan struct{}, 1), pending: map[uint64]chan frame{}, events: map[string]*eventStream{}, done: make(chan struct{}), wait: make(chan struct{})}
	go p.read(out)
	// Wait only after read has consumed stdout. Close always reaps this child.
	go func() { <-p.done; _ = cmd.Wait(); close(p.wait) }()
	// Publish before handshake I/O so forced shutdown can reach this process.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.stop(p)
		return nil, llm.ProviderUnavailable
	}
	c.p = p
	c.mu.Unlock()
	var hello struct {
		ProtocolVersion int `json:"protocol_version"`
	}
	if err := c.callProcess(ctx, p, "bridge.initialize", map[string]int{"protocol_version": ProtocolVersion}, &hello); err != nil || hello.ProtocolVersion != ProtocolVersion {
		c.stop(p)
		return nil, llm.ModelIncompatible
	}
	return p, nil
}

func (p *process) read(out io.Reader) {
	defer close(p.done)
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	for scanner.Scan() {
		var f frame
		if json.Unmarshal(scanner.Bytes(), &f) != nil || f.JSONRPC != "2.0" {
			p.fail(llm.InvalidProviderResponse)
			return
		}
		p.mu.Lock()
		if f.ID != 0 && f.Method == "" && (f.Error != nil || f.Result != nil) {
			if ch := p.pending[f.ID]; ch != nil {
				select {
				case ch <- f:
				default:
				}
			}
		} else if f.ID == 0 && f.Method == "generation.observation" {
			var header struct {
				GenerationID string `json:"generation_id"`
			}
			if json.Unmarshal(f.Params, &header) == nil {
				if stream := p.events[header.GenerationID]; stream != nil {
					stream.observe(f.Params)
				}
			}
		} else if f.ID == 0 && f.Method == "generation.event" {
			var e llm.GenerationEvent
			if json.Unmarshal(f.Params, &e) != nil {
				p.mu.Unlock()
				p.fail(llm.InvalidProviderResponse)
				return
			}
			if stream := p.events[e.GenerationID]; stream != nil {
				stream.accept(e)
			}
		} else {
			p.mu.Unlock()
			p.fail(llm.InvalidProviderResponse)
			return
		}
		p.mu.Unlock()
	}
	if scanner.Err() != nil {
		p.fail(llm.InvalidProviderResponse)
	}
}

func (p *process) fail(err error) { p.mu.Lock(); p.failure = err; p.mu.Unlock() }
func (p *process) err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		return p.failure
	}
	return llm.ProviderUnavailable
}

func (c *Client) stop(p *process) {
	p.stopOnce.Do(func() { _ = p.in.Close(); _ = p.cmd.Process.Kill() })
	<-p.wait
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		return nil
	}
	c.closed = true
	c.closeDone = make(chan struct{})
	done, p, pending := c.closeDone, c.p, c.initializing
	c.mu.Unlock()
	if p != nil {
		// An initializer cannot process shutdown before its handshake completes.
		// Force it immediately; established peers receive bounded graceful close.
		if pending == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = c.callProcess(ctx, p, "bridge.shutdown", struct{}{}, nil)
			cancel()
		}
		c.stop(p)
	}
	if pending != nil {
		<-pending.done
	}
	close(done)
	return nil
}

var errAcknowledgementTimeout = errors.New("provider RPC acknowledgement timeout")

func (c *Client) callProcess(ctx context.Context, p *process, method string, params, out any) error {
	_, err := c.exchange(ctx, p, method, params, out, nil)
	if err == errAcknowledgementTimeout {
		return llm.ProviderUnavailable
	}
	return err
}

// exchange reports complete dispatch separately from acknowledgement. A timed
// out response does not mean a generation request was never accepted by the peer.
// dispatchDone, when provided, also releases cancellation after failed admission.
func (c *Client) exchange(ctx context.Context, p *process, method string, params, out any, dispatchDone chan struct{}) (bool, error) {
	defer func() {
		if dispatchDone != nil {
			close(dispatchDone)
		}
	}()
	if ctx.Err() != nil {
		return false, llm.ProviderUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id := c.next.Add(1)
	data, err := json.Marshal(params)
	if err != nil {
		return false, llm.InvalidProviderResponse
	}
	wire, err := json.Marshal(frame{JSONRPC: "2.0", ID: id, Method: method, Params: data})
	if err != nil || len(wire) >= MaxFrameBytes {
		return false, llm.InvalidProviderResponse
	}
	wire = append(wire, '\n')
	ch := make(chan frame, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	select {
	case p.write <- struct{}{}:
	case <-ctx.Done():
		return false, llm.ProviderUnavailable
	case <-p.done:
		return false, p.err()
	}
	// One writer owns pipe deadlines. Cancellation interrupts the actual Write,
	// including a peer that has stopped reading. Join the callback before the next
	// writer can reset the deadline, so it cannot interrupt a subsequent request.
	var release func()
	if guard, ok := ctx.Value(dispatchGuardKey{}).(func(context.Context) (func(), error)); ok {
		release, err = guard(ctx)
	}
	if err == nil {
		err = c.write(ctx, p, wire)
	}
	if release != nil {
		release()
	}
	<-p.write
	if dispatchDone != nil {
		close(dispatchDone)
		dispatchDone = nil
	}
	if err != nil {
		return false, err
	}
	var f frame
	select {
	case f = <-ch:
	case <-ctx.Done():
		return true, errAcknowledgementTimeout
	case <-p.done:
		select {
		case f = <-ch:
		default:
			return true, p.err()
		}
	}
	if f.Error != nil {
		return true, llm.NormalizeError(f.Error.Message)
	}
	if out != nil && json.Unmarshal(f.Result, out) != nil {
		return true, llm.InvalidProviderResponse
	}
	return true, nil
}

func (c *Client) write(ctx context.Context, p *process, wire []byte) error {
	if ctx.Err() != nil {
		return llm.ProviderUnavailable
	}
	deadline, _ := ctx.Deadline()
	if err := p.in.SetWriteDeadline(deadline); err != nil {
		c.stop(p)
		return llm.ProviderUnavailable
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = p.in.SetWriteDeadline(time.Now()); close(interrupted) })
	_, err := p.in.Write(wire)
	if !stop() {
		<-interrupted
	}
	_ = p.in.SetWriteDeadline(time.Time{})
	if err != nil {
		// A partial JSON frame makes the stream unusable. Terminate this process;
		// do not replay this request or let another writer append to its prefix.
		c.stop(p)
		return llm.ProviderUnavailable
	}
	return nil
}

func (c *Client) cleanupGeneration(ctx context.Context, p *process, id string) error {
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer stop()
	err := c.callProcess(cleanup, p, "generation.cancel", map[string]string{"generation_id": id}, nil)
	if err != nil {
		c.stop(p)
	}
	return err
}

func (c *Client) cancelGeneration(ctx context.Context, p *process, id string) error {
	if err := c.cleanupGeneration(ctx, p, id); err != nil {
		return errors.Join(llm.GenerationCancelled, err)
	}
	return llm.GenerationCancelled
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	p, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	return c.callProcess(ctx, p, method, params, out)
}

func (c *Client) Generate(ctx context.Context, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return c.generate(ctx, "generation.start", r, sink)
}
func (c *Client) Continue(ctx context.Context, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (llm.GenerationResult, error) {
	return c.generate(ctx, "generation.continue", r, sink)
}

func (c *Client) generate(ctx context.Context, method string, r llm.GenerationRequest, sink func(llm.GenerationEvent)) (result llm.GenerationResult, err error) {
	started := time.Now()
	r.Finalize = nil // only this registration may install the Host commit gate
	var connection llm.Connection
	var completed *llm.GenerationEvent
	var stream *eventStream
	var retire func()
	hostRejected := false
	defer func() {
		if retire != nil {
			defer retire() // also retire if a Host callback panics
		}
		if stream != nil && stream.wasCancelled() && !hostRejected {
			result, err = llm.GenerationResult{}, llm.GenerationCancelled
		}
		if c.observer != nil {
			if recordErr := c.observer(context.WithoutCancel(ctx), r, connection, result, time.Since(started), err); recordErr != nil {
				result = llm.GenerationResult{}
				err = recordErr
			}
		}
		if retire != nil {
			retire()
		}
		// Account completion is provisional until Host persistence accepts it.
		// In particular, auth loss must not publish a completed event.
		if err == nil && completed != nil && sink != nil {
			sink(*completed)
		}
	}()
	c.mu.Lock()
	capture := c.dispatchObserver
	c.mu.Unlock()
	observe := capture != nil && r.OwnerToken != ""
	images, imageErr := resolveImages(ctx, r)
	if imageErr != nil {
		return llm.GenerationResult{}, imageErr
	}
	wire := r
	wire.Messages, err = llm.ResolveMemoryMessages(ctx, r)
	if err != nil {
		return llm.GenerationResult{}, err
	}
	var params any = struct {
		llm.GenerationRequest
		Images    map[string]imagePayload `json:"images,omitempty"`
		Observe   bool                    `json:"observe,omitempty"`
		UIPreview bool                    `json:"ui_preview,omitempty"`
	}{wire, images, observe, sink != nil}
	if c.resolver != nil {
		resolved, resolveErr := c.resolver(ctx, r.Snapshot)
		if resolveErr != nil {
			return llm.GenerationResult{}, resolveErr
		}
		connection = resolved
		params = struct {
			llm.GenerationRequest
			Connection llm.Connection          `json:"connection"`
			Images     map[string]imagePayload `json:"images,omitempty"`
			Observe    bool                    `json:"observe,omitempty"`
			UIPreview  bool                    `json:"ui_preview,omitempty"`
		}{wire, connection, images, observe, sink != nil}
	}
	if ctx.Err() != nil {
		return llm.GenerationResult{}, llm.GenerationCancelled
	}
	if r.GenerationID == "" || r.RunID == "" || r.SessionID != r.RunID || r.Snapshot.SnapshotID == "" || r.Snapshot.MaxOutputTokens <= 0 {
		return llm.GenerationResult{}, llm.InvalidProviderResponse
	}
	p, err := c.ensure(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return llm.GenerationResult{}, llm.GenerationCancelled
		}
		return llm.GenerationResult{}, err
	}
	stream = newEventStream(r.GenerationID, r.Snapshot.MaxOutputTokens)
	if observe {
		stream.capture = func(d llm.Dispatch) {
			for _, m := range r.Messages {
				if m.Memory != nil && d.Body != nil {
					d.Body = nil
					d.BodyHash = ""
					d.Omitted = "Memory-bearing request body omitted"
					break
				}
			}
			capture(r, d)
		}
		stream.wireSecrets = []string{connection.APIKey, connection.AccessToken}
	}
	if r.OutputLimitBytes > 0 && r.OutputLimitBytes < stream.remaining {
		stream.remaining = r.OutputLimitBytes
	}
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	if r.DispatchGuard != nil {
		dispatchCtx = context.WithValue(dispatchCtx, dispatchGuardKey{}, r.DispatchGuard)
	}
	defer stopDispatch()
	stream.interrupt = stopDispatch
	stream.sessionID = r.SessionID
	// Admission and terminal session disposal share the lifecycle lock.
	c.mu.Lock()
	if c.disposedSessions[r.SessionID] {
		c.mu.Unlock()
		return llm.GenerationResult{}, llm.GenerationCancelled
	}
	p.mu.Lock()
	if p.events[r.GenerationID] != nil {
		p.mu.Unlock()
		c.mu.Unlock()
		return llm.GenerationResult{}, llm.InvalidProviderResponse
	}
	p.events[r.GenerationID] = stream
	p.mu.Unlock()
	c.mu.Unlock()
	retire = func() {
		p.mu.Lock()
		<-stream.settleGate
		// Persistence may already have accepted or rejected the result. Without
		// persistence, retirement is the acceptance point (including Fake peers).
		if !stream.settled {
			if stream.wasCancelled() && !hostRejected && err == nil {
				result, err = llm.GenerationResult{}, llm.GenerationCancelled
			}
			stream.settled = true
		}
		if p.events[r.GenerationID] == stream {
			delete(p.events, r.GenerationID)
		}
		stream.settleGate <- struct{}{}
		p.mu.Unlock()
	}
	r.Finalize = func(commit func(bool) error) error {
		return stream.finalize(func(cancelled bool) error {
			return commit((cancelled || ctx.Err() != nil) && !hostRejected)
		})
	}
	dispatched, err := c.exchange(dispatchCtx, p, method, params, nil, stream.dispatchDone)
	if stream.wasCancelled() {
		return llm.GenerationResult{}, llm.GenerationCancelled
	}
	if ctx.Err() != nil {
		if dispatched {
			return llm.GenerationResult{}, c.cancelGeneration(ctx, p, r.GenerationID)
		}
		return llm.GenerationResult{}, llm.GenerationCancelled
	}
	if err != nil {
		if dispatched && err == errAcknowledgementTimeout {
			if cleanupErr := c.cleanupGeneration(ctx, p, r.GenerationID); cleanupErr != nil {
				return llm.GenerationResult{}, errors.Join(llm.ProviderUnavailable, cleanupErr)
			}
			return llm.GenerationResult{}, llm.ProviderUnavailable
		}
		select {
		case <-p.done:
			c.stop(p) // a failed reader cannot acknowledge further cleanup RPCs
		default:
		}
		return llm.GenerationResult{}, err
	}
	result = llm.GenerationResult{ToolCalls: []llm.ToolCall{}}
	var generatedText strings.Builder
	var authTick <-chan time.Time
	if connection.Guard != nil {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		authTick = ticker.C
	}
	for {
		if connection.Guard != nil {
			if authErr := connection.Guard(ctx); authErr != nil {
				hostRejected = true
				if cleanupErr := c.cleanupGeneration(ctx, p, r.GenerationID); cleanupErr != nil {
					return llm.GenerationResult{}, errors.Join(authErr, cleanupErr)
				}
				return llm.GenerationResult{}, authErr
			}
		}
		if stream.wasCancelled() {
			return llm.GenerationResult{}, llm.GenerationCancelled
		}
		if ctx.Err() != nil {
			return llm.GenerationResult{}, c.cancelGeneration(ctx, p, r.GenerationID)
		}
		var d eventDelivery
		select {
		case <-authTick:
			continue
		case <-stream.cancelled:
			return llm.GenerationResult{}, llm.GenerationCancelled
		case <-ctx.Done():
			continue
		case <-p.done:
			select {
			case d = <-stream.events:
			default:
				c.stop(p)
				return llm.GenerationResult{}, p.err()
			}
		case d = <-stream.events:
		}
		if stream.wasCancelled() {
			return llm.GenerationResult{}, llm.GenerationCancelled
		}
		if d.err != nil {
			if d.rejected {
				hostRejected = true
				if cleanupErr := c.cleanupGeneration(ctx, p, r.GenerationID); cleanupErr != nil {
					return llm.GenerationResult{}, errors.Join(d.err, cleanupErr)
				}
			}
			return llm.GenerationResult{}, d.err
		}
		e := d.event
		result.Clipped = result.Clipped || d.clipped
		switch e.Kind {
		case "text_delta":
			generatedText.WriteString(e.Text)
		case "tool_call":
			result.ToolCalls = append(result.ToolCalls, *e.ToolCall)
		case "completed":
			result.Text = generatedText.String()
			result.Usage = e.Usage
			result.WebSearch = e.WebSearch
			if r.WebSearch != nil && (result.WebSearch == nil || len(result.ToolCalls) != 0 || result.Clipped) {
				return llm.GenerationResult{}, llm.InvalidProviderResponse
			}
			if r.Snapshot.ProviderKind == "openai_account" {
				completed = &e
			} else if sink != nil {
				sink(e)
			}
			return result, nil
		}
		if sink != nil {
			sink(e)
		}
	}
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	stream := p.events[id]
	p.mu.Unlock()
	if stream != nil {
		// Registration precedes serialization and writer admission. Cancel must
		// follow that registration's write, but must not wait for its start ack.
		select {
		case <-stream.dispatchDone:
		case <-ctx.Done():
			return llm.ProviderUnavailable
		case <-p.done:
			return p.err()
		}
	}
	if err := c.callProcess(ctx, p, "generation.cancel", map[string]string{"generation_id": id}, nil); err != nil {
		return err
	}
	if stream != nil {
		return stream.cancel(ctx)
	}
	return nil
}

// Dispose is terminal for this Run's session. An acknowledgement settles all
// matching registrations, including pending start acks and queued completions.
func (c *Client) Dispose(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.disposedSessions == nil {
		c.disposedSessions = map[string]bool{}
	}
	c.disposedSessions[id] = true
	p.mu.Lock()
	targets := map[string]*eventStream{}
	for key, stream := range p.events {
		if stream.sessionID == id {
			targets[key] = stream
		}
	}
	p.mu.Unlock()
	c.mu.Unlock()
	// Every admitted write must precede the disposal RPC. Do not await start acks.
	for _, stream := range targets {
		select {
		case <-stream.dispatchDone:
		case <-ctx.Done():
			c.stop(p)
			return llm.ProviderUnavailable
		case <-p.done:
			return p.err()
		}
	}
	if err := c.callProcess(ctx, p, "session.dispose", map[string]string{"session_id": id}, nil); err != nil {
		c.stop(p) // Failed cleanup cannot leave a suppressed, unobservable generation.
		return err
	}
	for _, stream := range targets {
		if err := stream.cancel(ctx); err != nil {
			c.stop(p)
			return err
		}
	}
	return nil
}
func (c *Client) Start(ctx context.Context) (llm.AuthState, error) {
	var s llm.AuthState
	err := c.call(ctx, "auth.start", struct{}{}, &s)
	return s, err
}
func (c *Client) ContinueAuth(ctx context.Context, input string) (llm.AuthState, error) {
	var s llm.AuthState
	err := c.call(ctx, "auth.continue", map[string]string{"input": input}, &s)
	return s, err
}
func (c *Client) Refresh(ctx context.Context) (llm.AuthState, error) {
	var s llm.AuthState
	err := c.call(ctx, "auth.refresh", struct{}{}, &s)
	return s, err
}
func (c *Client) Logout(ctx context.Context) error {
	return c.call(ctx, "auth.logout", struct{}{}, nil)
}
func (c *Client) Status(ctx context.Context) (llm.AuthState, error) {
	var s llm.AuthState
	err := c.call(ctx, "auth.status", struct{}{}, &s)
	return s, err
}
func (c *Client) List(ctx context.Context) ([]llm.CatalogModel, error) {
	var s []llm.CatalogModel
	err := c.call(ctx, "models.list", struct{}{}, &s)
	return s, err
}
func (c *Client) RefreshCatalog(ctx context.Context) ([]llm.CatalogModel, error) {
	var s []llm.CatalogModel
	err := c.call(ctx, "models.refresh", struct{}{}, &s)
	return s, err
}

var _ llm.LLMProvider = (*Client)(nil)
var _ llm.ProviderAuth = (*Client)(nil)
var _ llm.ModelCatalog = (*Client)(nil)

// Run calls the versioned probe RPC for provider-side connection checks, then
// independently exercises the generation contract. Catalog claims cannot pass.
func (c *Client) Run(ctx context.Context, p llm.Profile) (llm.ProbeResult, error) {
	return c.RunWithProgress(ctx, p, nil)
}

func (c *Client) RunWithProgress(ctx context.Context, p llm.Profile, progress func(llm.ProbeStage)) (llm.ProbeResult, error) {
	if err := c.call(ctx, "model.probe", p, nil); err != nil {
		return llm.ProbeResult{}, err
	}
	return (llm.Prober{Provider: c}).RunWithProgress(ctx, p, progress)
}

var _ llm.ModelProbe = (*Client)(nil)
