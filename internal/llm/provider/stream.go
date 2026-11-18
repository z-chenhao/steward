package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	HTTPClient *http.Client
	mu         sync.Mutex
	sockets    map[string]*websocketSession
}

func (c *Client) directHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = c.HTTPClient
	}
	if client == nil {
		client = &http.Client{}
	}
	direct := *client
	direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &direct
}

func (c *Client) Stream(ctx context.Context, m Model, in Context, o Options, emit func(Event)) (AssistantMessage, error) {
	o = o.Resolve(m)
	m = resolveModel(m, o)
	if err := ValidateModel(m); err != nil {
		return AssistantMessage{}, err
	}
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	wire, err := BuildRequest(m, in, o)
	if err != nil {
		return AssistantMessage{}, err
	}
	if o.Transport != "" && o.Transport != "sse" && o.Transport != "auto" && o.Transport != "websocket" && o.Transport != "websocket-cached" {
		return AssistantMessage{}, ErrIncompatible
	}
	if o.Transport == "websocket" || o.Transport == "websocket-cached" || o.Transport == "auto" && m.API == CodexResponses {
		message, started, err := c.websocket(ctx, m, wire, o, emit)
		code := ErrorCode(err)
		if err == nil || started || ctx.Err() != nil || o.Transport != "auto" || code == "AUTH_REQUIRED" || code == "RATE_LIMITED" || code == "MODEL_NOT_FOUND" {
			return message, err
		}
	}
	body, err := json.Marshal(wire.Body)
	if err != nil {
		return AssistantMessage{}, ErrIncompatible
	}
	httpClient := c.directHTTPClient(o.HTTPClient)
	state := newStreamState(m, emit)
	observe := func(phase, attempt string, status int, err error) {
		if o.Observe != nil {
			code := ""
			if err != nil {
				code = ErrorCode(err)
			}
			obs := Observation{Phase: phase, Attempt: attempt, Status: status, Error: code, At: time.Now()}
			if phase == "started" {
				obs.Body = append([]byte(nil), body...)
			}
			o.Observe(obs)
		}
	}
	retries := o.MaxRetries
	if retries < 0 {
		retries = 0
	}
	for attempt := 0; attempt <= retries; attempt++ {
		attemptID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), attempt)
		requestCtx, cancel := context.WithCancel(ctx)
		req, e := http.NewRequestWithContext(requestCtx, http.MethodPost, wire.URL, bytes.NewReader(body))
		if e != nil {
			cancel()
			return state.message, ErrIncompatible
		}
		req.Header = wire.Headers.Clone()
		if m.API == BedrockConverse && o.AccessToken == "" {
			if e := signAWS(req, body, o.AWS, time.Now()); e != nil {
				cancel()
				return state.message, e
			}
		}
		observe("started", attemptID, 0, nil)
		var headerTimer *time.Timer
		if o.HeaderTimeout > 0 {
			headerTimer = time.AfterFunc(o.HeaderTimeout, func() { observe("header_timeout", attemptID, 0, nil); cancel() })
		}
		response, e := httpClient.Do(req)
		if headerTimer != nil {
			headerTimer.Stop()
		}
		if e != nil {
			phaseError := requestCtx.Err()
			cancel()
			observe("error", attemptID, 0, e)
			if ctx.Err() != nil {
				return state.message, ctx.Err()
			}
			if phaseError != nil {
				return state.message, &Error{Code: "PROVIDER_UNAVAILABLE"}
			}
			if attempt < retries {
				if e := retryWait(ctx, attempt, 0); e != nil {
					return state.message, e
				}
				continue
			}
			return state.message, &Error{Code: "PROVIDER_UNAVAILABLE"}
		}
		observe("headers", attemptID, response.StatusCode, nil)
		if o.OnResponse != nil {
			o.OnResponse(response)
		}
		idle := o.IdleTimeout
		var idleTimer *time.Timer
		if idle > 0 {
			idleTimer = time.AfterFunc(idle, func() { observe("stream_idle_timeout", attemptID, response.StatusCode, nil); cancel() })
		}
		limit := o.MaxResponseBytes
		if limit == 0 {
			limit = 64 << 20
		}
		reader := &observedReader{Reader: response.Body, max: limit, onRead: func(first bool) {
			if first {
				observe("first_body", attemptID, response.StatusCode, nil)
			}
			if idleTimer != nil {
				idleTimer.Reset(idle)
			}
		}}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failure := responseError(response, reader)
			if idleTimer != nil {
				idleTimer.Stop()
			}
			response.Body.Close()
			cancel()
			if attempt < retries && (response.StatusCode == 429 || response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504) {
				delay := failure.RetryAfter
				if o.MaxRetryDelay > 0 && delay > o.MaxRetryDelay {
					return state.message, failure
				}
				if e := retryWait(ctx, attempt, delay); e != nil {
					return state.message, e
				}
				continue
			}
			return state.message, failure
		}
		maxEvents := o.MaxEvents
		if maxEvents == 0 {
			maxEvents = 100000
		}
		count := 0
		firstOutput := false
		state.onOutput = func(kind string) {
			if !firstOutput {
				firstOutput = true
				observe(kind, attemptID, response.StatusCode, nil)
			}
		}
		handle := func(event string, data []byte) error {
			count++
			if count > maxEvents {
				return ErrInvalidResponse
			}
			if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
				return nil
			}
			var v map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if decoder.Decode(&v) != nil {
				return ErrInvalidResponse
			}
			if event != "" && v["type"] == nil {
				v["type"] = event
			}
			if o.OnProviderEvent != nil {
				if e := o.OnProviderEvent(v); e != nil {
					return e
				}
			}
			return state.consume(m.API, v)
		}
		if m.API == BedrockConverse {
			e = readEventStream(reader, handle)
		} else {
			e = readSSE(reader, handle)
		}
		if idleTimer != nil {
			idleTimer.Stop()
		}
		requestError := requestCtx.Err()
		response.Body.Close()
		cancel()
		if ctx.Err() != nil {
			return state.message, ctx.Err()
		}
		if e != nil {
			if ctx.Err() == nil && requestError != nil {
				return state.message, &Error{Code: "PROVIDER_UNAVAILABLE"}
			}
			var failure *Error
			if len(state.message.Content) > 0 && !errors.As(e, &failure) {
				return state.message, ErrInvalidResponse
			}
			return state.message, e
		}
		if !state.terminal {
			return state.message, ErrInvalidResponse
		}
		CalculateCost(m, &state.message.Usage)
		if e := state.finish(); e != nil {
			return state.message, e
		}
		observe("stream_terminal", attemptID, response.StatusCode, nil)
		return state.message, nil
	}
	return state.message, &Error{Code: "PROVIDER_UNAVAILABLE"}
}

func (c *Client) Complete(ctx context.Context, m Model, in Context, o Options) (AssistantMessage, error) {
	return c.Stream(ctx, m, in, o, nil)
}

type observedReader struct {
	io.Reader
	read, max int64
	onRead    func(bool)
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		first := r.read == 0
		r.read += int64(n)
		if r.read > r.max {
			return 0, ErrInvalidResponse
		}
		if r.onRead != nil {
			r.onRead(first)
		}
	}
	return n, err
}

func readSSE(r io.Reader, handle func(string, []byte) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 8<<20)
	var data bytes.Buffer
	event := ""
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		v := bytes.TrimSuffix(data.Bytes(), []byte("\n"))
		err := handle(event, v)
		data.Reset()
		event = ""
		return err
	}
	for s.Scan() {
		line := strings.TrimSuffix(s.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "event":
			event = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
			if data.Len() > 8<<20 {
				return ErrInvalidResponse
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	return flush()
}

func responseError(r *http.Response, body io.Reader) *Error {
	code := map[int]string{401: "AUTH_REQUIRED", 403: "AUTH_REQUIRED", 404: "MODEL_NOT_FOUND", 413: "CONTEXT_LENGTH_EXCEEDED", 429: "RATE_LIMITED", 500: "PROVIDER_UNAVAILABLE", 502: "PROVIDER_UNAVAILABLE", 503: "PROVIDER_UNAVAILABLE", 504: "PROVIDER_UNAVAILABLE"}[r.StatusCode]
	if code == "" {
		code = "MODEL_INCOMPATIBLE"
	}
	data, _ := io.ReadAll(io.LimitReader(body, 8193))
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if len(data) <= 8192 && json.Unmarshal(data, &v) == nil && v.Error.Code == "context_length_exceeded" {
		code = "CONTEXT_LENGTH_EXCEEDED"
	}
	delay := time.Duration(0)
	if n, err := strconv.ParseFloat(r.Header.Get("Retry-After"), 64); err == nil && n > 0 {
		delay = time.Duration(n * float64(time.Second))
	} else if t, e := http.ParseTime(r.Header.Get("Retry-After")); e == nil {
		delay = time.Until(t)
	}
	return &Error{Code: code, Status: r.StatusCode, RetryAfter: delay}
}
func retryWait(ctx context.Context, attempt int, delay time.Duration) error {
	if delay <= 0 {
		delay = time.Duration(50*(1<<min(attempt, 8))) * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
